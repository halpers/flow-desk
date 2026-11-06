"""Public HTTP/S3 acceptance on a new synthetic version in a disposable mock stack."""

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
    assert (
        settings["MODEL_PROVIDER"] == "mock"
        and settings["FILE_STORE_CLEANUP_ENABLED"].lower() == "true"
    )
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
            trust_env=False,
            timeout=15,
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
            result = response.json()
            assert response.status_code == expected, (
                path,
                response.status_code,
                result.get("code"),
            )
            return result.get("data")

        def login(account):
            data = call(
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )
            http.headers["Authorization"] = "Bearer " + data["access_token"]

        login("demo.admin")
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Source history " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        version = call(
            "POST",
            f"/knowledge-bases/{base['id']}/documents",
            {
                "title": "Source history acceptance",
                "filename": "policy.pdf",
                "content_base64": base64.b64encode(original).decode(),
            },
            202,
        )
        prefix = f"/documents/{version['document_id']}/versions/{version['id']}"
        copies = prefix + "/archive-copies"
        history = copies + "/history"

        def wait_storage():
            for _ in range(120):
                item = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ][0]
                if item["source_storage"] == "s3" and item["index_status"] == "ready":
                    return
                time.sleep(0.5)
            raise AssertionError("Archive timeout")

        retired = []
        cleanup_receipts = []
        for epoch in range(2):
            if epoch:
                call("POST", prefix + "/archive", {}, 202)
            wait_storage()
            call("POST", prefix + "/restore-source", {})
            plan = call("GET", copies)
            item = next(row for row in plan["items"] if row["source_epoch"] == epoch)
            assert item["eligible"] and plan["source_epoch"] == epoch + 1
            body = {"confirm": True, "expected_source_epoch": epoch + 1}
            key = str(uuid4())
            cleanup_path = copies + "/" + item["id"] + "/cleanup"
            call("POST", cleanup_path, body, 202, key)
            for _ in range(120):
                state = database.execute(
                    "select s.status,e.status from business.source_objects s join business.outbox_events e on e.tenant_id=s.tenant_id and e.payload->>'object_id'=s.id::text and e.topic='knowledge.source_cleanup' where s.id=%s",
                    (item["id"],),
                ).fetchone()
                if state == ("retired", "delivered"):
                    break
                time.sleep(0.5)
            else:
                raise AssertionError("Retirement/delivery timeout")
            retired.append(item)
            cleanup_receipts.append((cleanup_path, body, key))
        object_id = retired[0]["id"]
        target = copies + "/" + object_id + "/archive-metadata"
        body = {"confirm": True, "expected_source_epoch": 2}
        columns = "id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size,status,created_at,retired_at"
        before = database.execute(
            "select " + columns + " from business.source_objects where id=%s",
            (object_id,),
        ).fetchone()
        call("POST", target, {**body, "expected_source_epoch": 3}, 409)
        key = str(uuid4())
        receipt = call("POST", target, body, key=key)
        assert receipt == call("POST", target, body, key=key)
        assert (
            database.execute(
                "select count(*) from business.source_objects where id=%s", (object_id,)
            ).fetchone()[0]
            == 0
        )
        assert (
            database.execute(
                "select "
                + columns
                + " from business.source_object_archives where id=%s",
                (object_id,),
            ).fetchone()
            == before
        )
        assert (
            database.execute(
                "select count(*) from business.audit_events where operation='knowledge.source_metadata_archive' and object_id=%s",
                (object_id,),
            ).fetchone()[0]
            == 1
        )
        call("POST", target, body, 409)
        call("POST", target, {**body, "expected_source_epoch": 3}, 409, key)
        checks.append(
            {
                "name": "atomic hot-to-history move preserves every original identity field and immutable receipt; stale/new/conflicting attempts rejected",
                "passed": True,
            }
        )
        for old_path, old_body, old_key in cleanup_receipts:
            assert call("POST", old_path, old_body, 202, old_key)["status"] == "retired"
        source = call("GET", prefix + "/source")
        assert (
            source["content_hash"] == digest
            and base64.b64decode(source["content_base64"]) == original
        )
        inventory = call("GET", prefix + "/source-inventory?limit=50")
        assert len(inventory["items"]) == 2 and all(
            item["registered_status"] == "retired"
            and item["reconciliation"] == "retired_zero_bytes"
            and item["size"] == 0
            for item in inventory["items"]
        )
        checks.append(
            {
                "name": "real S3 retirement markers remain zero bytes and registered after archive; old cleanup receipts replay and database original matches SHA256",
                "passed": True,
            }
        )
        # Paging-only synthetic registrations: no storage objects or deletable candidates.
        database.execute(
            """insert into business.source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size,status,created_at,retired_at)
          select gen_random_uuid(),tenant_id,id,1000+n,'history-paging-test','history-paging-test-'||id::text||'-'||n,content_hash,source_size,'retired','2020-01-01'::timestamptz,'2020-01-02'::timestamptz from business.document_versions cross join generate_series(1,107)n where id=%s""",
            (version["id"],),
        )
        expected = database.execute(
            "select id::text from (select id,created_at from business.source_objects where version_id=%s union all select id,created_at from business.source_object_archives where version_id=%s) r order by created_at desc,id desc",
            (version["id"], version["id"]),
        ).fetchall()
        cursor = ""
        ids = []
        pages = 0
        while True:
            page = call("GET", history + "?limit=20&cursor=" + cursor)
            assert not any(
                name in json.dumps(page)
                for name in ("object_key", "store_id", "content_hash", "bucket")
            )
            ids.extend(item["id"] for item in page["items"])
            pages += 1
            if not page["has_more"]:
                assert not page["next_cursor"]
                break
            cursor = page["next_cursor"]
        assert (
            ids == [item[0] for item in expected]
            and len(ids) == 109
            and len(set(ids)) == 109
        )
        assert len(call("GET", copies)["items"]) == 101
        call("GET", history + "?cursor=invalid", expected=400)
        call("GET", history + "?limit=51", expected=400)
        checks.append(
            {
                "name": "109 registrations page continuously across hot/history with timestamp ties; daily preview remains bounded; cursor/limit errors rejected",
                "passed": True,
            }
        )
        login("demo.operator")
        call("GET", history, expected=403)
        call("POST", target, body, 403, key)
        checks.append(
            {
                "name": "ordinary operator cannot view privileged history or replay archive receipt",
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
                        "copy_id": retired[1]["id"],
                    },
                    indent=2,
                )
                + "\n"
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.14",
                "environment": "disposable local mock with real S3-compatible storage",
                "created_at": datetime.now(UTC).isoformat(),
                "history_rows": 109,
                "pages": pages,
                "paging_only_synthetic_rows": 107,
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Source history public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
