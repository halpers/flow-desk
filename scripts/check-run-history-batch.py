"""Public batch acceptance using only newly created synthetic mock tasks."""

import argparse
import json
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
import psycopg
from acceptance_http import login_with_backoff

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--browser-fixture", type=Path)
    args = parser.parse_args()
    assert args.env_file.resolve().is_relative_to(ROOT / "work")
    config = dict(
        line.split("=", 1)
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert config["MODEL_PROVIDER"] == "mock"
    origin = "http://localhost:" + config["WEB_HOST_PORT"]
    checks = []
    with (
        psycopg.connect(
            host="localhost",
            port=int(config["PG_HOST_PORT"]),
            dbname="flowdesk",
            user="flowdesk_owner",
            password=config["POSTGRES_PASSWORD"],
            autocommit=True,
            options="-c search_path=business,public",
            connect_timeout=5,
        ) as database,
        httpx.Client(
            base_url=origin + "/api/v1",
            timeout=25,
            trust_env=False,
            headers={"Origin": origin, "X-FlowDesk-Client": "web"},
        ) as http,
    ):

        def login(account):
            response = login_with_backoff(http, account, config["DEMO_PASSWORD"])
            assert response.status_code == 200, ("login", response.status_code)
            http.headers["Authorization"] = (
                "Bearer " + response.json()["data"]["access_token"]
            )

        def call(method, path, body=None, expected=200, key=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            assert response.status_code == expected, (
                method,
                path,
                response.status_code,
                response.json().get("code"),
            )
            if path.startswith("/run-history-batches"):
                assert response.headers.get("cache-control") == "no-store"
            return response.json().get("data")

        login("demo.operator")
        tasks = []
        snapshots = {}
        for _ in range(2):
            conversation = call(
                "POST",
                "/conversations",
                {"title": "Synthetic history batch " + str(uuid4())},
                201,
            )
            run = call(
                "POST",
                f"/conversations/{conversation['id']}/messages",
                {
                    "client_message_id": str(uuid4()),
                    "content": "查询 FD209901010023",
                    "attachments": [],
                },
                202,
            )
            path = "/runs/" + run["id"]
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                run = call("GET", path)
                if run["status"] == "failed":
                    lifecycle = call("GET", path + "/lifecycle")
                    if (
                        lifecycle["agent"]["available"]
                        and not lifecycle["agent"]["protection_reasons"]
                    ):
                        break
                time.sleep(0.3)
            else:
                raise AssertionError("new synthetic task did not settle")
            with database.cursor() as cursor:
                cursor.execute(
                    "UPDATE runs SET updated_at=now()-interval '31 days',lease_expires_at=NULL "
                    "WHERE id=%s AND conversation_id=%s AND status='failed' AND actor_user_id="
                    "(SELECT id FROM users WHERE account='demo.operator') RETURNING id",
                    (run["id"], conversation["id"]),
                )
                assert cursor.fetchone(), "Only this script's new task may be aged"
            tasks.append(run)
            snapshots[run["id"]] = {
                "run": call("GET", path),
                "messages": call(
                    "GET", f"/conversations/{conversation['id']}/messages"
                ),
                "conversation_id": conversation["id"],
            }
        ids = sorted(item["id"] for item in tasks)
        call("POST", "/run-history-batches/preview", {"run_ids": ids}, 403)
        call("GET", "/run-history-batches", expected=403)
        checks.append("operator cannot preview or list administrative batches")
        login("demo.admin")
        preview = call("POST", "/run-history-batches/preview", {"run_ids": ids})
        assert preview["can_create"] and preview["maximum_items"] == 10
        assert [item["run_id"] for item in preview["items"]] == ids
        body = {
            "items": [
                {
                    name: item[name]
                    for name in ("run_id", "expected_version", "expected_generation")
                }
                for item in preview["items"]
            ],
            "confirm": True,
        }
        call("POST", "/run-history-batches", {**body, "confirm": False}, 400)
        key = str(uuid4())
        receipt = call("POST", "/run-history-batches", body, key=key)
        batch_path = "/run-history-batches/" + receipt["batch_id"]
        batch = call("GET", batch_path)
        assert batch["completed_count"] == 0 and len(batch["items"]) == 2
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE run_id=ANY(%s::uuid[])",
                (ids,),
            )
            assert cursor.fetchone()[0] == 0, "registration must not move payloads"
            cursor.execute(
                "SELECT count(*) FROM run_history_batch_items WHERE batch_id=%s",
                (receipt["batch_id"],),
            )
            assert cursor.fetchone()[0] == 2
        checks.append(
            "confirmed registration atomically persists both original intents without archiving"
        )
        operation_ids = [item["operation_id"] for item in batch["items"]]
        first_path = batch_path + "/operations/" + operation_ids[0] + "/resume"
        batch = call("POST", first_path)
        assert batch["completed_count"] == 1 and batch["status"] == "pending"
        assert call("POST", "/run-history-batches", body, key=key) == receipt
        assert call("POST", first_path)["completed_count"] == 1
        assert any(
            item["batch_id"] == receipt["batch_id"]
            for item in call("GET", "/run-history-batches")["items"]
        )
        batch = call("POST", batch_path + "/operations/" + operation_ids[1] + "/resume")
        assert batch["completed_count"] == 2 and batch["status"] == "completed"
        assert [item["operation_id"] for item in batch["items"]] == operation_ids
        checks.append(
            "partial completion is durable and both resume and batch replay preserve identities"
        )
        login("demo.operator")
        call("GET", batch_path, expected=403)
        for run_id in ids:
            original = snapshots[run_id]
            assert call("GET", "/runs/" + run_id) == original["run"]
            assert (
                call("GET", f"/conversations/{original['conversation_id']}/messages")
                == original["messages"]
            )
            call(
                "POST",
                "/runs/" + run_id + "/retry",
                {"expected_version": original["run"]["version"]},
                409,
            )
        checks.append(
            "public task payloads remain unchanged and old execution stays fenced"
        )
        login("demo.admin")
        for run_id in ids:
            call(
                "POST",
                "/runs/" + run_id + "/history/restore",
                {
                    "expected_version": snapshots[run_id]["run"]["version"],
                    "expected_generation": 1,
                    "confirm": True,
                },
            )
        assert call("POST", "/run-history-batches", body, key=key) == receipt
        assert call("GET", batch_path)["completed_count"] == 2
        for run_id in ids:
            assert call("GET", "/runs/" + run_id + "/history")["state"] == "live"
        checks.append(
            "later single restoration does not alter old batch results or replay archival"
        )
        if args.browser_fixture:
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps({"run_ids": ids}, indent=2) + "\n", encoding="utf-8"
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "phase": "M7.26",
                "profile": "disposable-mock",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(json.dumps({"phase": "M7.26", "passed": len(checks)}))


if __name__ == "__main__":
    main()
