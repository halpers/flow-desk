"""Exercise the real scheduler in a new synthetic tenant of a disposable mock stack."""

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
    assert settings["MODEL_PROVIDER"] == "mock"
    assert settings.get("RUN_HISTORY_SCHEDULER_ENABLED", "false") == "true"
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    tenant = str(uuid4())
    suffix = uuid4().hex[:12]
    administrator, operator = "schedule.admin." + suffix, "schedule.operator." + suffix
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
        # Creating this isolated tenant is acceptance fixture setup only. All
        # policy changes and maintenance thereafter use the public Go API/worker.
        with database.cursor() as cursor:
            cursor.execute(
                "INSERT INTO tenants(id,code,name) VALUES(%s,%s,'Synthetic schedule acceptance')",
                (tenant, "schedule-" + suffix),
            )
            for account, role in ((administrator, "admin"), (operator, "operator")):
                cursor.execute(
                    "INSERT INTO users(id,tenant_id,account,password_hash,display_name,role,enabled) "
                    "SELECT %s,%s,%s,password_hash,'Synthetic schedule owner',%s,true FROM users WHERE account='demo.admin' RETURNING id",
                    (str(uuid4()), tenant, account, role),
                )
                assert cursor.fetchone()

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
                path,
                response.status_code,
                response.json().get("code"),
            )
            if "history" in path:
                assert response.headers.get("cache-control") == "no-store"
            return response.json().get("data")

        def due():
            with database.cursor() as cursor:
                cursor.execute(
                    "UPDATE run_history_schedules SET next_run_at=clock_timestamp()-interval '1 second' WHERE tenant_id=%s AND owner_user_id=(SELECT id FROM users WHERE account=%s)",
                    (tenant, administrator),
                )
                assert cursor.rowcount == 1

        login(operator)
        call("GET", "/run-history-schedule", expected=403)
        conversation = call(
            "POST", "/conversations", {"title": "Synthetic scheduled history"}, 201
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
            if run["status"] == "failed":
                lifecycle = call("GET", run_path + "/lifecycle")
                if (
                    lifecycle["agent"]["available"]
                    and not lifecycle["agent"]["protection_reasons"]
                ):
                    break
            time.sleep(0.3)
        else:
            raise AssertionError("Synthetic read-only task did not settle")
        messages_path = f"/conversations/{conversation['id']}/messages"
        original_messages = call("GET", messages_path)
        with database.cursor() as cursor:
            cursor.execute(
                "UPDATE runs SET updated_at=clock_timestamp()-interval '31 days',lease_expires_at=NULL WHERE tenant_id=%s AND id=%s AND status='failed'",
                (tenant, run["id"]),
            )
            assert cursor.rowcount == 1
            cursor.execute(
                "SELECT seq,payload_hash,payload FROM run_events WHERE tenant_id=%s AND run_id=%s ORDER BY seq",
                (tenant, run["id"]),
            )
            original_events = cursor.fetchall()
        login(administrator)
        initial = call("GET", "/run-history-schedule")
        assert not initial["enabled"] and initial["revision"] == 0
        body = {
            "enabled": True,
            "minimum_age_days": 30,
            "interval_minutes": 5,
            "expected_revision": 0,
            "confirm": True,
        }
        call("PUT", "/run-history-schedule", {**body, "confirm": False}, 400)
        key = str(uuid4())
        saved = call("PUT", "/run-history-schedule", body, key=key)
        assert saved["revision"] == 1 and saved["authorization_valid"]
        checks.append(
            "default-off isolated tenant policy requires current administrator and explicit confirmation"
        )
        due()
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            current = call("GET", "/run-history-schedule")
            if current["last_outcome"] == "completed":
                break
            time.sleep(0.4)
        else:
            raise AssertionError(
                "Real background worker did not complete scheduled archival"
            )
        operation = current["operation"]
        assert (
            operation["run_id"] == run["id"]
            and operation["generation"] == 1
            and operation["status"] == "completed"
        )
        assert call("GET", run_path + "/history")["state"] == "archived"
        with database.cursor() as cursor:
            cursor.execute(
                "SELECT count(*) FROM message_payload_archives WHERE tenant_id=%s AND run_id=%s",
                (tenant, run["id"]),
            )
            assert cursor.fetchone()[0] == len(original_messages["items"])
            cursor.execute(
                "SELECT seq,payload_hash,payload FROM run_events_history_read WHERE tenant_id=%s AND run_id=%s ORDER BY seq",
                (tenant, run["id"]),
            )
            assert cursor.fetchall() == original_events
            cursor.execute(
                "SELECT count(*) FROM audit_events WHERE tenant_id=%s AND object_id=%s AND source='scheduled'",
                (tenant, run["id"]),
            )
            assert cursor.fetchone()[0] == 2
        login(operator)
        assert call("GET", messages_path) == original_messages
        login(administrator)
        checks.append(
            "real worker losslessly archives original messages events and checkpoints through durable operation"
        )
        restored = call(
            "POST",
            run_path + "/history/restore",
            {
                "expected_version": run["version"],
                "expected_generation": 1,
                "confirm": True,
            },
        )
        assert restored["generation"] == 2
        # Two complete empty scans prove restored/fenced tasks are not selected.
        for _ in range(2):
            before = call("GET", "/run-history-schedule")["last_checked_at"]
            due()
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                current = call("GET", "/run-history-schedule")
                if current["last_checked_at"] != before:
                    break
                time.sleep(0.3)
            else:
                raise AssertionError("Scheduled empty scan did not finish")
            assert current["last_outcome"] == "empty"
        assert call("GET", run_path + "/history")["generation"] == 2
        checks.append(
            "manual restoration preserves execution fence and is excluded from automatic re-archival"
        )
        disabled = call(
            "PUT",
            "/run-history-schedule",
            {**body, "enabled": False, "expected_revision": 1},
        )
        assert disabled["revision"] == 2 and not disabled["enabled"]
        assert call("PUT", "/run-history-schedule", body, key=key) == saved
        current = call("GET", "/run-history-schedule")
        assert current["revision"] == 2 and not current["enabled"]
        checks.append(
            "disable persists and old save receipt replay cannot re-enable or overwrite current policy"
        )
        # Credential-version changes invalidate grants without a fabricated session.
        call("PUT", "/run-history-schedule", {**body, "expected_revision": 2})
        with database.cursor() as cursor:
            cursor.execute(
                "UPDATE users SET auth_version=auth_version+1 WHERE tenant_id=%s AND account=%s",
                (tenant, administrator),
            )
        due()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            with database.cursor() as cursor:
                cursor.execute(
                    "SELECT last_outcome FROM run_history_schedules WHERE tenant_id=%s",
                    (tenant,),
                )
                if cursor.fetchone()[0] == "authorization_required":
                    break
            time.sleep(0.3)
        else:
            raise AssertionError("Revoked durable authorization was not rejected")
        login(administrator)
        current = call("GET", "/run-history-schedule")
        assert not current["authorization_valid"]
        call(
            "PUT",
            "/run-history-schedule",
            {**body, "enabled": False, "expected_revision": 3},
        )
        checks.append(
            "credential-version revocation stops background authorization and explicit current save can disable it"
        )
        if args.browser_fixture:
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "account": administrator,
                        "operator": operator,
                        "tenant_id": tenant,
                        "run_id": run["id"],
                    }
                )
                + "\n",
                encoding="utf-8",
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "phase": "M7.30",
                "profile": "isolated-tenant-disposable-mock",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(f"Scheduled history acceptance passed: {len(checks)} checks")


if __name__ == "__main__":
    main()
