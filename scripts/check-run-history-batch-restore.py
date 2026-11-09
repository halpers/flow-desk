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
            if path.startswith(
                ("/run-history-batches", "/run-history-restore-batches")
            ):
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
        call("POST", "/run-history-restore-batches/preview", {"run_ids": ids}, 403)
        login("demo.admin")
        live_preview = call(
            "POST", "/run-history-restore-batches/preview", {"run_ids": ids}
        )
        assert not live_preview["can_create"] and all(
            not item["can_restore"] for item in live_preview["items"]
        )

        def request_body(preview):
            return {
                "items": [
                    {
                        name: item[name]
                        for name in (
                            "run_id",
                            "expected_version",
                            "expected_generation",
                        )
                    }
                    for item in preview["items"]
                ],
                "confirm": True,
            }

        archive_preview = call("POST", "/run-history-batches/preview", {"run_ids": ids})
        archive_body = request_body(archive_preview)
        archive_key = str(uuid4())
        archived = call("POST", "/run-history-batches", archive_body, key=archive_key)
        archive_path = "/run-history-batches/" + archived["batch_id"]
        for item in call("GET", archive_path)["items"]:
            call(
                "POST", archive_path + "/operations/" + item["operation_id"] + "/resume"
            )
        assert call("GET", archive_path)["target"] == "archived"
        login("demo.operator")
        call("POST", "/run-history-restore-batches/preview", {"run_ids": ids}, 403)
        call(
            "POST",
            "/run-history-restore-batches",
            {
                "confirm": True,
                "items": [
                    {
                        "run_id": run_id,
                        "expected_version": snapshots[run_id]["run"]["version"],
                        "expected_generation": 1,
                    }
                    for run_id in ids
                ],
            },
            403,
        )
        login("demo.admin")
        preview = call("POST", "/run-history-restore-batches/preview", {"run_ids": ids})
        assert preview["can_create"] and preview["maximum_items"] == 10
        assert [item["run_id"] for item in preview["items"]] == ids
        assert all(
            item["can_restore"] and item["expected_generation"] == 1
            for item in preview["items"]
        )
        checks.append(
            "live tasks are ineligible and only administrators can preview restoration"
        )
        body = request_body(preview)
        call("POST", "/run-history-restore-batches", {**body, "confirm": False}, 400)
        key = str(uuid4())
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE run_id=ANY(%s::uuid[])",
                (ids,),
            )
            cold_count = cursor.fetchone()[0]
            assert cold_count > 0
        receipt = call("POST", "/run-history-restore-batches", body, key=key)
        batch_path = "/run-history-batches/" + receipt["batch_id"]
        batch = call("GET", batch_path)
        assert (
            batch["target"] == "live"
            and batch["completed_count"] == 0
            and len(batch["items"]) == 2
        )
        assert all(
            item["target"] == "live" and item["generation"] == 2
            for item in batch["items"]
        )
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE run_id=ANY(%s::uuid[])",
                (ids,),
            )
            assert cursor.fetchone()[0] == cold_count, (
                "registration must not restore payloads"
            )
        checks.append(
            "confirmed registration persists both intents without moving cold payloads"
        )
        operation_ids = [item["operation_id"] for item in batch["items"]]
        first_path = batch_path + "/operations/" + operation_ids[0] + "/resume"
        batch = call("POST", first_path)
        assert batch["completed_count"] == 1 and batch["status"] == "pending"
        assert call("POST", "/run-history-restore-batches", body, key=key) == receipt
        assert call("POST", first_path)["completed_count"] == 1
        assert any(
            item["batch_id"] == receipt["batch_id"] and item["target"] == "live"
            for item in call("GET", "/run-history-batches")["items"]
        )
        batch = call("POST", batch_path + "/operations/" + operation_ids[1] + "/resume")
        assert batch["completed_count"] == 2 and batch["status"] == "completed"
        assert [item["operation_id"] for item in batch["items"]] == operation_ids
        checks.append(
            "partial restoration is durable and continuations retain original operation identities"
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
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE run_id=ANY(%s::uuid[])",
                (ids,),
            )
            assert cursor.fetchone()[0] == 0
        checks.append(
            "public payloads are unchanged and permanent execution fences still reject retry"
        )
        login("demo.admin")
        call(
            "POST",
            "/runs/" + ids[0] + "/history/archive",
            {
                "expected_version": snapshots[ids[0]]["run"]["version"],
                "expected_generation": 2,
                "confirm": True,
            },
        )
        assert call("POST", "/run-history-restore-batches", body, key=key) == receipt
        assert (
            call("POST", "/run-history-batches", archive_body, key=archive_key)
            == archived
        )
        assert call("GET", batch_path)["completed_count"] == 2
        assert call("GET", "/runs/" + ids[0] + "/history")["state"] == "archived"
        assert call("GET", "/runs/" + ids[1] + "/history")["state"] == "live"
        checks.append(
            "later re-archival preserves old restoration receipts without repeating restoration"
        )
        if args.browser_fixture:
            call(
                "POST",
                "/runs/" + ids[1] + "/history/archive",
                {
                    "expected_version": snapshots[ids[1]]["run"]["version"],
                    "expected_generation": 2,
                    "confirm": True,
                },
            )
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps({"run_ids": ids}, indent=2) + "\n", encoding="utf-8"
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "phase": "M7.27",
                "profile": "disposable-mock",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(json.dumps({"phase": "M7.27", "passed": len(checks)}))


if __name__ == "__main__":
    main()
