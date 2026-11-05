"""Public history acceptance; writes synthetic fixtures only in disposable work/ stacks."""

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
    assert args.env_file.resolve().is_relative_to(ROOT / "work")
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
                assert result["code"] == code
            return result.get("data")

        def login(account):
            result = call(
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )
            http.headers["Authorization"] = "Bearer " + result["access_token"]

        login("demo.admin")
        base_name = "History archive " + str(uuid4())[:8]
        base = call(
            "POST",
            "/knowledge-bases",
            {"name": base_name, "read_roles": ["admin", "operator"]},
            201,
        )
        rows = []
        for number in range(4):
            body = {
                "filename": f"history-{number}.txt",
                "content": f"Synthetic history policy revision {number}",
            }
            if not rows:
                body["title"] = "History archive acceptance"
                path = f"/knowledge-bases/{base['id']}/documents"
            else:
                path = f"/documents/{rows[0]['document_id']}/versions"
            rows.append(call("POST", path, body, 202))
        document = rows[0]["document_id"]
        for _ in range(90):
            page = call("GET", f"/documents/{document}/versions")
            if len(page["items"]) == 4 and all(
                row["index_status"] == "ready" for row in page["items"]
            ):
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Synthetic indexing timeout")
        for number, row in enumerate(rows):
            database.execute(
                "UPDATE business.document_versions SET created_at=now()-interval '40 days'+%s*interval '1 minute' WHERE id=%s",
                (number, row["id"]),
            )
        version = rows[0]["id"]
        path = f"/documents/{document}/versions/{version}/archive-metadata"
        body = {"confirm": True, "expected_document_version": 1}
        call("POST", path, body, 409, code="METADATA_ARCHIVE_PROTECTED")
        key = str(uuid4())
        cleanup_path = f"/documents/{document}/versions/{version}/cleanup"
        call("POST", cleanup_path, body, 202, key=key)
        for _ in range(90):
            page = call("GET", f"/documents/{document}/history")
            candidate = next(row for row in page["items"] if row["id"] == version)
            if candidate["can_archive"]:
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Durable cleanup timeout")
        checks.append(
            {
                "name": "retained protection and real durable cleanup before archive",
                "passed": True,
            }
        )
        archive_key = str(uuid4())
        receipt = call("POST", path, body, key=archive_key)
        assert receipt == call("POST", path, body, key=archive_key)
        call(
            "POST",
            path,
            {"confirm": True, "expected_document_version": 2},
            409,
            key=archive_key,
            code="IDEMPOTENCY_CONFLICT",
        )
        page = call("GET", f"/documents/{document}/history")
        archived = next(row for row in page["items"] if row["id"] == version)
        assert (
            archived["metadata_archived"]
            and archived["filename"] == rows[0]["filename"]
        )
        assert (
            archived["archived_at"] == receipt["archived_at"]
            and not archived["can_archive"]
        )
        assert version not in {
            row["id"] for row in call("GET", f"/documents/{document}/versions")["items"]
        }
        assert database.execute(
            "SELECT filename,chunk_count,metadata_archived FROM business.document_versions WHERE id=%s",
            (version,),
        ).fetchone() == ("", 0, True)
        assert (
            database.execute(
                "SELECT count(*) FROM business.audit_events WHERE operation='knowledge.metadata_archive' AND object_id=%s",
                (version,),
            ).fetchone()[0]
            == 1
        )
        call(
            "GET",
            f"/documents/{document}/versions/{version}/source",
            expected=409,
            code="VERSION_RETIRED",
        )
        call(
            "POST",
            f"/documents/{document}/activate",
            {"version_id": version, "expected_version": 1},
            409,
            code="VERSION_RETIRED",
        )
        assert (
            database.execute(
                "SELECT count(*) FROM agent.knowledge_tombstones WHERE version_id=%s",
                (version,),
            ).fetchone()[0]
            == 1
        )
        checks.append(
            {
                "name": "atomic metadata archive, immutable replay, preserved history and fences",
                "passed": True,
            }
        )
        # Bulk historical fixture only, scoped to the document created by this invocation.
        database.execute(
            """INSERT INTO business.document_versions
            (id,tenant_id,document_id,job_id,filename,content,content_hash,index_status,chunk_count,cleanup_state,source_size,created_at)
            SELECT gen_random_uuid(),tenant_id,id,gen_random_uuid(),'synthetic-history-'||n||'.txt','','synthetic-hash','ready',1,'purged',1,'2020-01-01T00:00:00Z'::timestamptz
            FROM business.documents CROSS JOIN generate_series(1,107) n WHERE id=%s""",
            (document,),
        )
        expected_ids = [
            str(row[0])
            for row in database.execute(
                "SELECT id FROM business.document_versions WHERE document_id=%s ORDER BY created_at DESC,id DESC",
                (document,),
            ).fetchall()
        ]
        actual_ids = []
        cursor = ""
        for _ in range(30):
            page = call(
                "GET", f"/documents/{document}/history?limit=11&cursor={cursor}"
            )
            actual_ids.extend(row["id"] for row in page["items"])
            if not page["has_more"]:
                assert not page["next_cursor"]
                break
            cursor = page["next_cursor"]
        assert actual_ids == expected_ids and len(actual_ids) == 111
        call("GET", f"/documents/{document}/history?limit=51", expected=400)
        call("GET", f"/documents/{document}/history?cursor=broken", expected=400)
        checks.append(
            {
                "name": "111 history rows, equal timestamp tie-breaker and bounded cursor requests",
                "passed": True,
            }
        )
        if args.browser_fixture:
            first_page = call("GET", f"/documents/{document}/history")
            selected = next(row for row in first_page["items"] if row["can_archive"])
            args.browser_fixture.parent.mkdir(parents=True, exist_ok=True)
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "base_name": base_name,
                        "document_id": document,
                        "version_id": selected["id"],
                        "filename": selected["filename"],
                    }
                )
                + "\n",
                encoding="utf-8",
            )
        login("demo.operator")
        call("GET", f"/documents/{document}/history", expected=403)
        call("POST", path, body, 403, key=archive_key)
        checks.append(
            {
                "name": "ordinary account cannot read history or replay metadata archive",
                "passed": True,
            }
        )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.10",
                "environment": "disposable local mock",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(f"History-archive public API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
