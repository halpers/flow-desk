"""Public policy acceptance using only this run's synthetic disposable data."""

import argparse
import json
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
import psycopg

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--browser-fixture", type=Path)
    args = parser.parse_args()
    assert args.env_file.resolve().is_relative_to(ROOT / "work"), (
        "Disposable work/ configuration required"
    )
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings["MODEL_PROVIDER"] == "mock"
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
            connect_timeout=5,
        ) as database,
        httpx.Client(
            base_url=origin + "/api/v1",
            trust_env=False,
            timeout=15,
            headers={"Origin": origin, "X-FlowDesk-Client": "web"},
        ) as http,
    ):

        def call(method, path, body=None, expected=200, key=None, code=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            result = response.json()
            assert response.status_code == expected, (
                path,
                response.status_code,
                result.get("code"),
            )
            if code:
                assert result["code"] == code, (path, result["code"])
            return result.get("data")

        def login(account):
            result = call(
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )
            http.headers["Authorization"] = "Bearer " + result["access_token"]

        login("demo.admin")
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Retention policy " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        path = f"/knowledge-bases/{base['id']}/retention-policy"
        policy = call("GET", path)
        assert policy == {
            "knowledge_base_id": base["id"],
            "keep_latest": 3,
            "minimum_age_days": 30,
            "revision": 1,
        }
        call(
            "PUT",
            path,
            {"keep_latest": 0, "minimum_age_days": 30, "expected_revision": 1},
            400,
            code="INVALID_ARGUMENT",
        )
        rows = []
        for number in range(4):
            body = {
                "filename": f"policy-{number}.txt",
                "content": f"Synthetic retention rule revision {number}.",
            }
            upload_path = (
                f"/documents/{rows[0]['document_id']}/versions"
                if rows
                else f"/knowledge-bases/{base['id']}/documents"
            )
            if not rows:
                body["title"] = "Retention policy acceptance"
            rows.append(call("POST", upload_path, body, 202))
        document = rows[0]["document_id"]
        for _ in range(90):
            versions = call("GET", f"/documents/{document}/versions")["items"]
            if all(row["index_status"] == "ready" for row in versions):
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Indexing timeout")
        for number, row in enumerate(rows):
            database.execute(
                "update business.document_versions set created_at=now()-interval '40 days'+%s*interval '1 minute' where id=%s",
                (number, row["id"]),
            )
        plan_path = f"/documents/{document}/retention"
        plan = call("GET", plan_path)
        assert (
            plan["policy_revision"] == 1
            and sum(row["eligible"] for row in plan["items"]) == 1
        )
        key = str(uuid4())
        body = {"keep_latest": 4, "minimum_age_days": 90, "expected_revision": 1}
        updated = call("PUT", path, body, key=key)
        assert updated["revision"] == 2
        assert call("PUT", path, body, key=key) == updated
        call("PUT", path, body, 409, code="RETENTION_POLICY_CHANGED")
        cleanup_path = f"/documents/{document}/versions/{rows[0]['id']}/cleanup"
        cleanup = {
            "confirm": True,
            "expected_document_version": 1,
            "expected_policy_revision": 1,
        }
        call("POST", cleanup_path, cleanup, 409, code="RETENTION_POLICY_CHANGED")
        call(
            "POST",
            cleanup_path,
            {"confirm": True, "expected_document_version": 1},
            409,
            code="RETENTION_POLICY_CHANGED",
        )
        plan = call("GET", plan_path)
        assert plan["policy_revision"] == 2 and not any(
            row["eligible"] for row in plan["items"]
        )
        checks.append(
            {
                "name": "default policy, range validation, durable replay and stale cleanup fence",
                "passed": True,
            }
        )
        call(
            "PUT",
            path,
            {"keep_latest": 1, "minimum_age_days": 1, "expected_revision": 2},
        )
        assert call("PUT", path, body, key=key) == updated
        assert call("GET", path)["revision"] == 3
        plan = call("GET", plan_path)
        assert (
            plan["keep_latest"] == 1
            and sum(row["eligible"] for row in plan["items"]) == 3
        )
        cleanup["expected_policy_revision"] = 3
        cleanup_key = str(uuid4())
        call("POST", cleanup_path, cleanup, 202, key=cleanup_key)
        call(
            "PUT",
            path,
            {"keep_latest": 3, "minimum_age_days": 30, "expected_revision": 3},
        )
        for _ in range(90):
            versions = call("GET", f"/documents/{document}/versions")["items"]
            if (
                next(row for row in versions if row["id"] == rows[0]["id"])[
                    "cleanup_state"
                ]
                == "purged"
            ):
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Confirmed cleanup did not recover")
        assert (
            call("POST", cleanup_path, cleanup, 202, key=cleanup_key)["cleanup_state"]
            == "purged"
        )
        call(
            "POST",
            cleanup_path,
            {"confirm": True, "expected_document_version": 1},
            409,
            code="RETENTION_POLICY_CHANGED",
        )
        checks.append(
            {
                "name": "custom candidates, immutable policy receipt and accepted cleanup surviving later policy",
                "passed": True,
            }
        )
        count = database.execute(
            "select count(*) from business.audit_events where operation='knowledge.retention_policy' and object_id=%s",
            (base["id"],),
        ).fetchone()[0]
        assert count == 3
        checks.append(
            {
                "name": "each accepted policy revision audited once without replayed mutation",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "base_name": base["name"],
                        "knowledge_base_id": base["id"],
                        "document_id": document,
                        "version_id": rows[1]["id"],
                        "policy_revision": 4,
                    },
                    indent=2,
                )
                + "\n",
                encoding="utf-8",
            )
        login("demo.operator")
        call("GET", path, expected=403)
        call("PUT", path, body, 403, key=key)
        checks.append(
            {
                "name": "operator cannot read, update or replay policy writes",
                "passed": True,
            }
        )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.9",
                "environment": "disposable local mock",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(f"Retention-policy public API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
