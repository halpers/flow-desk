"""Create a new isolated tenant and synthetic objects ONLY in a disposable work/ mock stack."""

import argparse
import base64
import hashlib
import json
import os
import subprocess
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
    assert settings["FILE_STORE_TENANT_INVENTORY_ENABLED"].lower() == "true"
    assert settings["FILE_STORE_INVENTORY_ENABLED"].lower() == "true"
    original = (ROOT / "flow-desk-agent/tests/fixtures/policy.pdf").read_bytes()
    digest = hashlib.sha256(original).hexdigest()
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    tenant, user = uuid4(), uuid4()
    account = "inventory." + user.hex[:16]
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
        # New tenant avoids dependence on other acceptance fixtures and their objects.
        with database.transaction():
            database.execute(
                "INSERT INTO business.tenants(id,code,name) VALUES(%s,%s,'Synthetic inventory tenant')",
                (tenant, "inventory-" + tenant.hex[:16]),
            )
            database.execute(
                "INSERT INTO business.users(id,tenant_id,account,password_hash,display_name,role) SELECT %s,%s,%s,password_hash,'Synthetic inventory admin','admin' FROM business.users WHERE account='demo.admin'",
                (user, tenant, account),
            )

        def call(method, path, body=None, expected=200):
            response = http.request(
                method, path, json=body, headers={"Idempotency-Key": str(uuid4())}
            )
            result = response.json()
            assert response.status_code == expected, (
                path.split("?")[0],
                response.status_code,
                result.get("code"),
            )
            if (
                method == "GET"
                and path.startswith("/source-inventory")
                and expected == 200
            ):
                assert response.headers["cache-control"] == "no-store"
            return result.get("data")

        def login(name):
            data = call(
                "POST",
                "/auth/login",
                {"account": name, "password": settings["DEMO_PASSWORD"]},
            )
            http.headers["Authorization"] = "Bearer " + data["access_token"]

        login(account)
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Tenant inventory acceptance",
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        version = call(
            "POST",
            f"/knowledge-bases/{base['id']}/documents",
            {
                "title": "Tenant inventory known version",
                "filename": "policy.pdf",
                "content_base64": base64.b64encode(original).decode(),
            },
            202,
        )
        for _ in range(120):
            row = call("GET", f"/documents/{version['document_id']}/versions")["items"][
                0
            ]
            if row["source_storage"] == "s3" and row["index_status"] == "ready":
                break
            time.sleep(0.25)
        else:
            raise AssertionError("Archive timeout")

        if os.name == "nt":
            work = args.env_file.resolve().parent
            linux = "/mnt/" + work.drive[0].lower() + work.as_posix()[2:]
            binary = "/mnt/c/work/go/src/flow-desk/work/source-store.test"
            command = ["wsl.exe", "--cd", linux, "--exec", "docker", "compose"]
        else:
            binary = str(ROOT / "work/source-store.test")
            command = ["docker", "compose"]
        command += [
            "-f",
            "docker-compose.yaml",
            "-f",
            "deploy/compose.storage.yaml",
            "run",
            "--rm",
            "--no-deps",
            "--volume",
            binary + ":/tmp/source-store.test:ro",
            "--env",
            "FLOWDESK_S3_SANDBOX_TESTS=1",
            "--env",
            "MODEL_PROVIDER=mock",
            "--env",
            "FLOWDESK_S3_TEST_TENANT=" + str(tenant),
            "--env",
            "FLOWDESK_S3_TEST_VERSION=" + version["id"],
            "--env",
            "FLOWDESK_S3_TEST_HASH=" + digest,
            "--entrypoint",
            "/tmp/source-store.test",
            "api",
            "-test.run",
            "^TestS3SandboxTenantInventoryFixture$",
            "-test.v",
        ]
        probe = subprocess.run(command, cwd=ROOT, capture_output=True, check=False)
        assert probe.returncode == 0, (
            "Disposable S3 fixture probe failed; examine local sandbox"
        )

        before = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        first = call("GET", "/source-inventory?limit=20")
        assert (
            first["scanned_count"] == 20
            and first["has_more"]
            and not first["content_verified"]
        )
        second = call(
            "GET", "/source-inventory?limit=20&cursor=" + first["next_cursor"]
        )
        items = first["items"] + second["items"]
        assert second["scanned_count"] == 5 and not second["has_more"]
        assert len(items) == 25 and len({item["object_ref"] for item in items}) == 25
        assert sum(item["association"] == "unknown_version" for item in items) == 22
        assert sum(item["association"] == "unrecognized_key" for item in items) == 1
        assert sum(item["reconciliation"] == "version_mismatch" for item in items) == 1
        assert sum(item["reconciliation"] == "current" for item in items) == 1
        assert all(
            not item["version_id"] and not item["document_id"]
            for item in items
            if item["association"] != "known_version"
        )
        raw = json.dumps([first, second])
        assert all(
            value not in raw
            for value in (
                "knowledge/",
                "object_key",
                "store_id",
                digest,
                settings["FILE_STORE_BUCKET"],
                settings["FILE_STORE_SECRET_KEY"],
            )
        )
        assert (
            database.execute(
                "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == before
        )
        checks.append(
            {
                "name": "25 real tenant objects paginated; unknown versions and nonstandard/hash-mismatched objects classified; foreign prefix excluded; no locator disclosure or registration",
                "passed": True,
            }
        )
        call("GET", "/source-inventory?cursor=malformed", expected=400)
        call("GET", "/source-inventory?limit=51", expected=400)
        login("demo.admin")
        call("GET", "/source-inventory?cursor=" + first["next_cursor"], expected=400)
        login("demo.operator")
        call("GET", "/source-inventory", expected=403)
        login(account)
        checks.append(
            {
                "name": "invalid page/cursor, cross-tenant cursor and ordinary operator rejected",
                "passed": True,
            }
        )
        # Simulate a restricted legacy ACL ONLY on this new synthetic tenant/base.
        database.execute(
            "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
            (json.dumps(["operator"]), tenant, base["id"]),
        )
        cursor = ""
        visible = []
        for _ in range(30):
            page = call("GET", "/source-inventory?limit=1&cursor=" + cursor)
            visible.extend(page["items"])
            if not page["has_more"]:
                break
            assert page["next_cursor"] != cursor
            cursor = page["next_cursor"]
        assert len(visible) == 23 and all(
            item["association"] != "known_version" for item in visible
        )
        # Restore only the newly created fixture's ACL for the browser scenario.
        database.execute(
            'UPDATE business.knowledge_bases SET read_roles=\'["admin","operator"]\',acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s',
            (tenant, base["id"]),
        )
        source = call(
            "GET",
            f"/documents/{version['document_id']}/versions/{version['id']}/source",
        )
        assert base64.b64decode(source["content_base64"]) == original
        checks.append(
            {
                "name": "ACL-restricted known versions hidden while empty pages advance; original SHA256 retained",
                "passed": True,
            }
        )
        count = database.execute(
            "SELECT count(*) FROM business.audit_events WHERE operation='knowledge.tenant_source_inventory' AND tenant_id=%s",
            (tenant,),
        ).fetchone()[0]
        assert count == 27
        checks.append(
            {
                "name": "only 27 successful pages audited; failed requests create no success audit",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "account": account,
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
                "stage": "M7.13",
                "environment": "disposable local mock with S3",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Tenant inventory public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
