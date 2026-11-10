"""Verify real overdue investigation automation in an isolated synthetic tenant."""

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
    assert settings.get("INVESTIGATION_AUTOMATION_ENABLED") == "true"
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    tenant, suffix = str(uuid4()), uuid4().hex[:12]
    administrator, operator = (
        "automation.admin." + suffix,
        "automation.operator." + suffix,
    )
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
        # Fixture setup only: creates new tenant and old snapshots through capture
        # triggers. No storage objects, real tenant or existing snapshots are edited.
        database.execute(
            "INSERT INTO tenants(id,code,name) VALUES(%s,%s,'Synthetic automation acceptance')",
            (tenant, "automation-" + suffix),
        )
        users = {}
        for account, role in ((administrator, "admin"), (operator, "operator")):
            users[account] = str(uuid4())
            database.execute(
                "INSERT INTO users(id,tenant_id,account,password_hash,display_name,role,enabled) "
                "SELECT %s,%s,%s,password_hash,'Synthetic automation owner',%s,true FROM users WHERE account='demo.admin'",
                (users[account], tenant, account, role),
            )
        refs = []
        for status, age in (
            ("investigating", 8),
            ("open", 8),
            ("closed", 8),
            ("investigating", 0),
        ):
            investigation, ref = str(uuid4()), uuid4().hex + uuid4().hex
            refs.append(ref)
            database.execute(
                "INSERT INTO source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,created_by,updated_by,updated_at) "
                "VALUES(%s,%s,%s,'synthetic-fixture',%s,%s,'Synthetic note: <b>keep literal</b>',%s,%s,clock_timestamp()-%s*interval '1 day')",
                (
                    investigation,
                    tenant,
                    ref,
                    "knowledge/" + tenant + "/misc/" + investigation,
                    status,
                    users[administrator],
                    users[administrator],
                    age,
                ),
            )

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
            assert response.headers.get("cache-control") == "no-store"
            return response.json().get("data")

        def due():
            database.execute(
                "UPDATE investigation_automation_policies SET next_run_at=clock_timestamp()-interval '1 second' WHERE tenant_id=%s",
                (tenant,),
            )

        def wait_outcome(outcome):
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                row = database.execute(
                    "SELECT last_outcome,next_run_at>clock_timestamp() FROM investigation_automation_policies WHERE tenant_id=%s",
                    (tenant,),
                ).fetchone()
                if row and row[0] == outcome and row[1]:
                    return
                time.sleep(0.25)
            raise AssertionError("Background outcome not confirmed: " + outcome)

        login(operator)
        call("GET", "/investigation-automation", expected=403)
        login(administrator)
        policy = call("GET", "/investigation-automation")
        assert (
            not policy["enabled"]
            and policy["revision"] == 0
            and policy["runtime_enabled"]
        )
        body = {
            "enabled": True,
            "idle_days": 7,
            "interval_minutes": 5,
            "expected_revision": 0,
            "confirm": True,
        }
        call("PUT", "/investigation-automation", {**body, "confirm": False}, 400)
        call("PUT", "/investigation-automation", {**body, "idle_days": 0}, 400)
        key = str(uuid4())
        original_policy = call("PUT", "/investigation-automation", body, key=key)
        assert (
            original_policy["revision"] == 1 and original_policy["authorization_valid"]
        )
        checks.append(
            "default-off policy requires current tenant administrator and explicit bounded confirmation"
        )
        path = "/source-investigations/" + refs[0]
        before = call("GET", path)
        due()
        wait_outcome("returned")
        after = call("GET", path)
        assert (
            after["status"] == "open"
            and after["revision"] == 2
            and after["note"] == before["note"]
        )
        history = call("GET", path + "/history")
        assert len(history["items"]) == 2
        assert [item["status"] for item in history["items"]] == [
            "open",
            "investigating",
        ]
        assert all(item["note"] == before["note"] for item in history["items"])
        assert (
            database.execute(
                "SELECT count(*) FROM audit_events WHERE tenant_id=%s AND operation='knowledge.investigation_auto_return' AND source='scheduled'",
                (tenant,),
            ).fetchone()[0]
            == 1
        )
        checks.append(
            "real background worker atomically returns overdue investigation with original note history and scheduled audit"
        )
        for _ in range(2):
            due()
            wait_outcome("empty")
        for ref, status in zip(
            refs[1:], ("open", "closed", "investigating"), strict=True
        ):
            current = call("GET", "/source-investigations/" + ref)
            assert current["revision"] == 1 and current["status"] == status
        assert call("GET", path)["revision"] == 2
        checks.append(
            "newly updated open and closed investigations are excluded and repeated scans do not create duplicate revisions"
        )
        disabled = call(
            "PUT",
            "/investigation-automation",
            {**body, "enabled": False, "expected_revision": 1},
        )
        assert not disabled["enabled"] and disabled["revision"] == 2
        assert (
            call("PUT", "/investigation-automation", body, key=key) == original_policy
        )
        assert not call("GET", "/investigation-automation")["enabled"]
        call("PUT", "/investigation-automation", body, 409)
        checks.append(
            "disable persists while old receipt replay preserves original result without re-enabling current policy"
        )
        call("PUT", "/investigation-automation", {**body, "expected_revision": 2})
        database.execute(
            "UPDATE users SET auth_version=auth_version+1 WHERE tenant_id=%s AND id=%s",
            (tenant, users[administrator]),
        )
        due()
        wait_outcome("authorization_required")
        login(administrator)
        revoked = call("GET", "/investigation-automation")
        assert not revoked["authorization_valid"]
        call(
            "PUT",
            "/investigation-automation",
            {**body, "enabled": False, "expected_revision": revoked["revision"]},
        )
        checks.append(
            "credential-version change invalidates persistent authorization until explicit current administrator reconfirmation"
        )
        if args.browser_fixture:
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps({"account": administrator}, indent=2) + "\n",
                encoding="utf-8",
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "phase": "M7.31",
                "profile": "isolated-tenant-disposable-mock",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(f"Investigation automation acceptance passed: {len(checks)} checks")


if __name__ == "__main__":
    main()
