"""Exercise manual investigation records using an isolated tenant in a disposable mock stack."""

import argparse
import base64
import json
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
    parser.add_argument("--inventory-fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--browser-fixture", type=Path)
    args = parser.parse_args()
    for path in (args.env_file, args.inventory_fixture):
        assert path.resolve().is_relative_to(ROOT / "work")
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings["MODEL_PROVIDER"] == "mock"
    fixture = json.loads(args.inventory_fixture.read_text())
    assert fixture["account"].startswith("inventory.")
    checks = []
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
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

        def call(method, path, body=None, expected=200, key=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            envelope = response.json()
            assert response.status_code == expected, (
                method,
                path.split("?")[0],
                response.status_code,
                envelope.get("code"),
            )
            if path.startswith("/source-investigations/"):
                assert response.headers.get("cache-control") == "no-store"
            return envelope.get("data")

        def login(account):
            response = login_with_backoff(http, account, settings["DEMO_PASSWORD"])
            envelope = response.json()
            assert response.status_code == 200, (
                "login",
                response.status_code,
                envelope.get("code"),
            )
            http.headers["Authorization"] = "Bearer " + envelope["data"]["access_token"]

        login(fixture["account"])
        tenant = database.execute(
            "SELECT tenant_id FROM business.users WHERE account=%s",
            (fixture["account"],),
        ).fetchone()[0]
        before = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        page = call("GET", "/source-inventory?limit=50")
        assert not page["has_more"]
        unknown = [
            item for item in page["items"] if item["association"] == "unknown_version"
        ]
        known = next(
            item for item in page["items"] if item["reconciliation"] == "current"
        )
        assert len(unknown) >= 3
        target = unknown[0]
        path = "/source-investigations/" + target["object_ref"]
        draft = call("GET", path)
        assert draft["revision"] == 0 and not draft["note"]
        key = str(uuid4())
        body = {
            "selection_token": target["investigation_token"],
            "expected_revision": 0,
            "status": "investigating",
            "note": "Synthetic review: source owner remains unconfirmed",
            "confirm": True,
        }
        receipt = call("PUT", path, body, key=key)
        assert (
            receipt["revision"] == 1
            and receipt["note"] == body["note"]
            and receipt["updated_by"]
        )
        assert call("PUT", path, body, key=key) == receipt
        checks.append(
            {
                "name": "unknown tenant object requires scoped inventory selection; confirmed create and lost-response replay have one revision",
                "passed": True,
            }
        )

        update = {
            **body,
            "selection_token": "",
            "expected_revision": 1,
            "status": "closed",
            "note": "Synthetic follow-up ended; no ownership or deletion decision",
        }
        latest = call("PUT", path, update)
        assert latest["revision"] == 2 and call("GET", path) == latest
        assert call("PUT", path, body, key=key) == receipt
        call("PUT", path, update, expected=409)
        call("PUT", path, {**body, "note": "Changed request"}, expected=409, key=key)
        call(
            "PUT",
            path,
            {**update, "expected_revision": 2, "confirm": False},
            expected=400,
        )
        unused_path = "/source-investigations/" + unknown[1]["object_ref"]
        call("PUT", unused_path, {**body, "selection_token": ""}, expected=400)
        call(
            "PUT",
            unused_path,
            {**body, "selection_token": target["investigation_token"]},
            expected=400,
        )
        login("demo.operator")
        call("GET", path, expected=403)
        call("PUT", path, body, expected=403, key=key)
        login(fixture["account"])
        checks.append(
            {
                "name": "immutable old receipt survives later revision; stale edits, changed-key body, missing/mismatched selection, unconfirmed write and operator access rejected",
                "passed": True,
            }
        )

        known_path = "/source-investigations/" + known["object_ref"]
        known_key = str(uuid4())
        known_body = {
            **body,
            "selection_token": known["investigation_token"],
            "note": "Synthetic known-version review",
        }
        call("PUT", known_path, known_body, key=known_key)
        base_id = database.execute(
            "SELECT knowledge_base_id FROM business.documents WHERE tenant_id=%s AND id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()[0]
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base_id),
            )
            call("GET", known_path, expected=404)
            call("PUT", known_path, known_body, expected=404, key=known_key)
            call(
                "PUT",
                known_path,
                {**known_body, "expected_revision": 1, "note": "Unauthorized edit"},
                expected=404,
            )
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["admin", "operator"]), tenant, base_id),
            )
        assert call("PUT", known_path, known_body, key=known_key)["revision"] == 1
        checks.append(
            {
                "name": "known-version current ACL applies to reads, new revisions and original receipt replay; fixture ACL restored",
                "passed": True,
            }
        )

        assert (
            database.execute(
                "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == before
        )
        source = call(
            "GET",
            f"/documents/{fixture['document_id']}/versions/{fixture['version_id']}/source",
        )
        assert (
            base64.b64decode(source["content_base64"])
            == (ROOT / "flow-desk-agent/tests/fixtures/policy.pdf").read_bytes()
        )
        assert (
            database.execute(
                "SELECT count(*) FROM business.source_investigations WHERE tenant_id=%s",
                (tenant,),
            ).fetchone()[0]
            == 2
        )
        assert (
            database.execute(
                "SELECT count(*) FROM business.api_idempotency WHERE tenant_id=%s AND operation='knowledge.source_investigate'",
                (tenant,),
            ).fetchone()[0]
            == 3
        )
        summaries = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigate'",
            (tenant,),
        ).fetchall()
        assert len(summaries) == 3
        assert all(
            value not in json.dumps(summaries)
            for value in (
                body["note"],
                update["note"],
                known_body["note"],
                "knowledge/",
                target["investigation_token"],
            )
        )
        raw = json.dumps([receipt, latest])
        assert all(
            value not in raw
            for value in (
                "object_key",
                "store_id",
                "selection_token",
                "knowledge/",
                settings["FILE_STORE_BUCKET"],
            )
        )
        checks.append(
            {
                "name": "two records, three atomic audits and receipts; private locators/notes absent from audit, source registration and original PDF unchanged",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps(
                    {**fixture, "object_ref": unknown[1]["object_ref"]}, indent=2
                )
                + "\n",
                encoding="utf-8",
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.15",
                "environment": "disposable local mock with S3",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(
        f"Source investigation public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
