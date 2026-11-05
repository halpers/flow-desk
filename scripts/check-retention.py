"""Local disposable-stack acceptance. Never ages or deletes existing business data."""

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
        "Disposable work/ env required"
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
            assert response.status_code == expected, (path, response.status_code)
            result = response.json()
            if code:
                assert result["code"] == code, (path, result["code"])
            return result.get("data")

        login = call(
            "POST",
            "/auth/login",
            {"account": "demo.admin", "password": settings["DEMO_PASSWORD"]},
        )
        http.headers["Authorization"] = "Bearer " + login["access_token"]
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Retention acceptance " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            expected=201,
        )

        def versions(title):
            rows = []
            for number in range(4):
                body = {
                    "filename": f"retention-{number}.txt",
                    "content": f"Synthetic retention policy revision {number}.",
                }
                path = (
                    f"/documents/{rows[0]['document_id']}/versions"
                    if rows
                    else f"/knowledge-bases/{base['id']}/documents"
                )
                if not rows:
                    body["title"] = title
                row = call("POST", path, body, 202)
                rows.append(row)
            document = rows[0]["document_id"]
            for _ in range(90):
                state = call("GET", f"/documents/{document}/versions")["items"]
                if all(row["index_status"] == "ready" for row in state):
                    break
                time.sleep(0.5)
            else:
                raise AssertionError("Indexing timeout")
            for number, row in enumerate(rows):
                # Only IDs created above are touched, and only in work/ mock environments.
                database.execute(
                    "UPDATE business.document_versions SET created_at=now()-interval '40 days'+%s*interval '1 minute' WHERE id=%s",
                    (number, row["id"]),
                )
            return rows

        rows = versions("Retention API fixture")
        candidate = rows[0]
        document, version = candidate["document_id"], candidate["id"]
        path = f"/documents/{document}/versions/{version}/cleanup"
        body = {"confirm": True, "expected_document_version": 1}
        plan = call("GET", f"/documents/{document}/retention")
        assert plan["keep_latest"] == 3 and plan["minimum_age_days"] == 30
        assert sum(row["eligible"] for row in plan["items"]) == 1
        call(
            "POST",
            f"/documents/{document}/activate",
            {"version_id": version, "expected_version": 1},
        )
        call("POST", path, body, 409, code="VERSION_CONFLICT")
        plan = call("GET", f"/documents/{document}/retention")
        assert (
            "active"
            in next(row for row in plan["items"] if row["version_id"] == version)[
                "reasons"
            ]
        )
        call(
            "POST",
            path,
            {"confirm": True, "expected_document_version": 2},
            409,
            code="VERSION_PROTECTED",
        )
        call("POST", f"/documents/{document}/withdraw", {"expected_version": 2})
        checks.append(
            {
                "name": "retention policy, activation and stale-preview protection",
                "passed": True,
            }
        )
        body["expected_document_version"] = 3
        key = str(uuid4())
        result = call("POST", path, body, 202, key=key)
        assert result["cleanup_state"] in ("cleanup_pending", "purged")
        for _ in range(90):
            result = call("POST", path, body, 202, key=key)
            if result["cleanup_state"] == "purged":
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Cleanup timeout")
        call(
            "GET",
            f"/documents/{document}/versions/{version}/source",
            expected=409,
            code="VERSION_RETIRED",
        )
        call(
            "POST",
            f"/documents/{document}/activate",
            {"version_id": version, "expected_version": 3},
            409,
            code="VERSION_RETIRED",
        )
        stored = database.execute(
            "SELECT content,octet_length(source_bytes),page_texts,source_size,content_hash FROM business.document_versions WHERE id=%s",
            (version,),
        ).fetchone()
        assert stored == (
            "",
            0,
            [],
            candidate["source_size"],
            candidate["content_hash"],
        )
        for table in (
            "knowledge_chunks",
            "knowledge_manifests",
            "semantic_vectors",
            "semantic_manifests",
        ):
            assert (
                database.execute(
                    f"SELECT count(*) FROM agent.{table} WHERE version_id=%s",
                    (version,),
                ).fetchone()[0]
                == 0
            )
        assert (
            database.execute(
                "SELECT count(*) FROM agent.knowledge_tombstones WHERE version_id=%s",
                (version,),
            ).fetchone()[0]
            == 1
        )
        assert (
            database.execute(
                "SELECT count(*) FROM business.audit_events WHERE operation='knowledge.cleanup' AND object_id=%s",
                (version,),
            ).fetchone()[0]
            == 1
        )
        checks.append(
            {
                "name": "durable cleanup, immutable metadata, index tombstone and idempotent replay",
                "passed": True,
            }
        )
        if args.browser_fixture:
            fixture_rows = versions("Retention browser fixture")
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "base_id": base["id"],
                        "base_name": base["name"],
                        "document_id": fixture_rows[0]["document_id"],
                        "version_id": fixture_rows[0]["id"],
                    },
                    indent=2,
                )
                + "\n"
            )
        login = call(
            "POST",
            "/auth/login",
            {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]},
        )
        http.headers["Authorization"] = "Bearer " + login["access_token"]
        call("GET", f"/documents/{document}/retention", expected=403)
        call("POST", path, body, 403, key=key)
        checks.append(
            {
                "name": "operator denial on preview and cleanup replay",
                "passed": True,
            }
        )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.7",
                "environment": "disposable local mock",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Retention public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
