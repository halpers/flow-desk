"""Inspect synthetic S3 generations in a disposable stack; never run on business data."""

import argparse
import base64
import hashlib
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
    assert args.env_file.resolve().is_relative_to(ROOT / "work")
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings["MODEL_PROVIDER"] == "mock"
    assert settings["FILE_STORE_INVENTORY_ENABLED"].lower() == "true"
    original = (ROOT / "flow-desk-agent/tests/fixtures/policy.pdf").read_bytes()
    digest = hashlib.sha256(original).hexdigest()
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
            timeout=15,
            trust_env=False,
            headers={"Origin": origin, "X-FlowDesk-Client": "web"},
        ) as http,
    ):

        def call(method, path, body=None, expected=200, code=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": str(uuid4())},
            )
            result = response.json()
            assert response.status_code == expected, (
                path.split("?")[0],
                response.status_code,
                result.get("code"),
            )
            if code:
                assert result["code"] == code
            if method == "GET" and "/source-inventory" in path and expected == 200:
                assert response.headers["cache-control"] == "no-store"
            return result.get("data")

        def login(account):
            data = call(
                "POST",
                "/auth/login",
                {
                    "account": account,
                    "password": settings["DEMO_PASSWORD"],
                },
            )
            http.headers["Authorization"] = "Bearer " + data["access_token"]

        login("demo.admin")
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Source inventory " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        version = call(
            "POST",
            f"/knowledge-bases/{base['id']}/documents",
            {
                "title": "Source inventory acceptance",
                "filename": "policy.pdf",
                "content_base64": base64.b64encode(original).decode(),
            },
            202,
        )
        tenant_id = database.execute(
            "SELECT tenant_id FROM business.knowledge_bases WHERE id=%s",
            (base["id"],),
        ).fetchone()[0]
        prefix = f"/documents/{version['document_id']}/versions/{version['id']}"
        path = prefix + "/source-inventory"

        def wait_archived():
            for _ in range(120):
                row = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ][0]
                if row["source_storage"] == "s3" and row["index_status"] == "ready":
                    return row
                time.sleep(0.25)
            raise AssertionError("Archive timeout")

        wait_archived()
        # Simulate a pre-ledger object, ONLY for the new version created above.
        removed = database.execute(
            "DELETE FROM business.source_objects WHERE tenant_id=%s AND version_id=%s "
            "AND source_epoch=0 AND status='registered' RETURNING id",
            (tenant_id, version["id"]),
        ).fetchall()
        assert len(removed) == 1
        for _ in range(21):
            call("POST", prefix + "/restore-source", {})
            call("POST", prefix + "/archive", {}, 202)
            wait_archived()

        def registrations():
            return database.execute(
                "SELECT id,status,source_epoch,object_key,source_size FROM business.source_objects "
                "WHERE tenant_id=%s AND version_id=%s ORDER BY id",
                (tenant_id, version["id"]),
            ).fetchall()

        before = registrations()
        current = wait_archived()
        first = call("GET", path + "?limit=20")
        assert len(first["items"]) == 20 and first["has_more"]
        second = call("GET", path + "?limit=20&cursor=" + first["next_cursor"])
        items = first["items"] + second["items"]
        assert len(items) == 22 and len({item["object_ref"] for item in items}) == 22
        assert not second["has_more"] and not second["next_cursor"]
        assert sum(item["reconciliation"] == "unregistered" for item in items) == 1
        assert sum(item["reconciliation"] == "current" for item in items) == 1
        assert all(item["size"] == len(original) for item in items)
        assert not first["content_verified"] and first["source_size"] == len(original)
        serialized = json.dumps([first, second])
        assert not any(
            value in serialized
            for value in (
                "knowledge/",
                "object_key",
                "store_id",
                settings["FILE_STORE_BUCKET"],
                settings["FILE_STORE_SECRET_KEY"],
            )
        )
        assert registrations() == before and wait_archived() == current
        checks.append(
            {
                "name": "22 real S3 objects in two pages; unregistered and current copies reconciled without adoption or source changes",
                "passed": True,
            }
        )

        one = call("GET", path + "?limit=1")
        next_one = call("GET", path + "?limit=1&cursor=" + one["next_cursor"])
        assert one["items"][0]["object_ref"] != next_one["items"][0]["object_ref"]
        call("GET", path + "?cursor=malformed", expected=400)
        call("GET", path + "?limit=51", expected=400)
        call("GET", path + "?cursor=" + first["next_cursor"][:-12], expected=400)
        checks.append(
            {
                "name": "encrypted cursor resumes; invalid cursor and oversized page rejected; no storage locator exposed",
                "passed": True,
            }
        )

        call("POST", prefix + "/restore-source", {})
        call(
            "GET",
            path + "?cursor=" + first["next_cursor"],
            expected=409,
            code="SOURCE_CHANGED",
        )
        data = call("GET", prefix + "/source")
        assert (
            data["content_hash"] == digest
            and base64.b64decode(data["content_base64"]) == original
        )
        call("POST", prefix + "/archive", {}, 202)
        wait_archived()
        checks.append(
            {
                "name": "restore invalidates prior inventory cursor and original bytes retain SHA256",
                "passed": True,
            }
        )

        login("demo.operator")
        call("GET", path, expected=403)
        count = database.execute(
            "SELECT count(*) FROM business.audit_events WHERE object_id=%s "
            "AND operation='knowledge.source_inventory'",
            (version["id"],),
        ).fetchone()[0]
        assert count == 4
        checks.append(
            {
                "name": "ordinary operator denied; only four successful inspected pages recorded in audit",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "base_name": base["name"],
                        "document_id": version["document_id"],
                        "version_id": version["id"],
                    },
                    indent=2,
                )
                + "\n"
            )

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.11",
                "environment": "disposable local mock with S3",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Source inventory public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
