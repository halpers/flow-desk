"""Lossless history acceptance; mutates only its new task in a disposable mock stack."""

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
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings["MODEL_PROVIDER"] == "mock", (
        "Requires disposable synthetic mock stack"
    )
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    checks = []
    with (
        psycopg.connect(
            host="localhost",
            port=int(settings["PG_HOST_PORT"]),
            dbname="flowdesk",
            user="flowdesk_owner",
            password=settings["POSTGRES_PASSWORD"],
            autocommit=True,
            options="-c search_path=business,public",
            connect_timeout=5,
        ) as database,
        httpx.Client(
            base_url=origin + "/api/v1",
            timeout=15,
            trust_env=False,
            headers={"Origin": origin, "X-FlowDesk-Client": "web"},
        ) as http,
    ):

        def login(account):
            response = login_with_backoff(http, account, settings["DEMO_PASSWORD"])
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
                response.status_code,
                response.json().get("code"),
            )
            if "/history" in path:
                assert response.headers.get("cache-control") == "no-store"
            return response.json().get("data")

        login("demo.operator")
        conversation = call(
            "POST",
            "/conversations",
            {"title": "Synthetic run history " + str(uuid4())},
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
        run_path = "/runs/" + run["id"]
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            run = call("GET", run_path)
            if run["status"] in {"failed", "succeeded", "cancelled", "expired"}:
                preview = call("GET", run_path + "/lifecycle")
                if (
                    preview["agent"]["available"]
                    and not preview["agent"]["protection_reasons"]
                ):
                    break
            time.sleep(0.3)
        else:
            raise AssertionError(
                "Synthetic task did not settle callbacks and model reservations"
            )
        assert run["status"] == "failed", (
            "Unknown synthetic order must fail without business effects"
        )
        messages_path = f"/conversations/{conversation['id']}/messages"
        original_messages = call("GET", messages_path)
        original_run = call("GET", run_path)
        with database.cursor() as cursor:
            cursor.execute(
                "UPDATE runs SET updated_at=now()-interval '31 days',lease_expires_at=NULL "
                "WHERE id=%s AND actor_user_id="
                "(SELECT id FROM users WHERE account='demo.operator') "
                "AND status='failed' RETURNING id",
                (run["id"],),
            )
            assert cursor.fetchone(), (
                "Only this freshly created synthetic task may be aged"
            )
            cursor.execute(
                "SELECT seq,payload_hash,payload FROM run_events WHERE run_id=%s ORDER BY seq",
                (run["id"],),
            )
            original_events = cursor.fetchall()
        # The clock adjustment is acceptance setup, not archive behavior.
        original_run = call("GET", run_path)
        call(
            "POST",
            run_path + "/history/archive",
            {
                "expected_version": run["version"],
                "expected_generation": 0,
                "confirm": True,
            },
            expected=403,
        )
        checks.append("ordinary operator cannot authorize maintenance")
        login("demo.admin")
        history = call("GET", run_path + "/history")
        assert history["can_archive"] and history["state"] == "live", history
        archive_key = str(uuid4())
        body = {
            "expected_version": run["version"],
            "expected_generation": 0,
            "confirm": True,
        }
        archived = call("POST", run_path + "/history/archive", body, key=archive_key)
        assert archived["status"] == "completed" and archived["generation"] == 1
        assert (
            call("POST", run_path + "/history/archive", body, key=archive_key)
            == archived
        )
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE run_id=%s",
                (run["id"],),
            )
            assert cursor.fetchone()[0] == len(original_messages["items"])
            cursor.execute(
                "SELECT count(*) FROM run_event_payload_archives WHERE run_id=%s",
                (run["id"],),
            )
            assert cursor.fetchone()[0] == len(original_events)
            cursor.execute(
                "SELECT count(*) FROM messages WHERE run_id=%s AND content<>''",
                (run["id"],),
            )
            assert cursor.fetchone()[0] == 0
            cursor.execute(
                "SELECT seq,payload_hash,payload FROM run_events_history_read "
                "WHERE run_id=%s ORDER BY seq",
                (run["id"],),
            )
            assert cursor.fetchall() == original_events
        checks.append(
            "hot payloads physically tiered; original events and hashes remain readable"
        )
        login("demo.operator")
        assert call("GET", messages_path) == original_messages
        assert call("GET", run_path) == original_run
        counts = call("GET", run_path + "/lifecycle")
        assert counts["business"]["event_count"] == len(original_events)
        assert (
            counts["agent"]["checkpoint_count"] == preview["agent"]["checkpoint_count"]
        )
        call("POST", run_path + "/retry", {"expected_version": run["version"]}, 409)
        checks.append(
            "authorized public reads/counts preserved and retry permanently fenced"
        )
        login("demo.admin")
        restored = call(
            "POST",
            run_path + "/history/restore",
            {
                "expected_version": run["version"],
                "expected_generation": 1,
                "confirm": True,
            },
        )
        assert restored["generation"] == 2 and restored["status"] == "completed"
        assert (
            call("POST", run_path + "/history/archive", body, key=archive_key)
            == archived
        )
        assert call("GET", run_path + "/history")["state"] == "live"
        login("demo.operator")
        assert call("GET", messages_path) == original_messages
        assert call("GET", run_path) == original_run
        call("POST", run_path + "/retry", {"expected_version": run["version"]}, 409)
        checks.append(
            "restore is byte-preserving storage recovery; old receipt replay has no new effect"
        )
        if args.browser_fixture:
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "run_id": run["id"],
                        "conversation_id": conversation["id"],
                        "version": run["version"],
                        "generation": 2,
                    },
                    indent=2,
                )
                + "\n",
                encoding="utf-8",
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "phase": "M7.23",
                "profile": "disposable-mock",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(json.dumps({"phase": "M7.23", "passed": len(checks)}))


if __name__ == "__main__":
    main()
