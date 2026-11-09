"""Check a tenant investigation worklist in a disposable mock stack using synthetic fixtures."""

import argparse
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
        successes = 0

        def call(method, path, body=None, expected=200):
            nonlocal successes
            response = http.request(
                method, path, json=body, headers={"Idempotency-Key": str(uuid4())}
            )
            envelope = response.json()
            assert response.status_code == expected, (
                method,
                path.split("?")[0],
                response.status_code,
                envelope.get("code"),
            )
            if method == "GET" and path.split("?")[0] == "/source-investigations":
                assert response.headers.get("cache-control") == "no-store"
                if expected == 200:
                    successes += 1
                    raw = json.dumps(envelope)
                    assert all(
                        value not in raw
                        for value in (
                            "object_key",
                            "store_id",
                            "selection_token",
                            "knowledge/",
                            "PRIVATE_WORKLIST",
                            '"note"',
                        )
                    )
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

        def all_pages(status="", limit=11):
            items = []
            cursor = ""
            empty_middle = 0
            for _ in range(150):
                page = call(
                    "GET",
                    f"/source-investigations?limit={limit}&status={status}&cursor={cursor}",
                )
                items.extend(page["items"])
                if not page["has_more"]:
                    assert not page["next_cursor"]
                    return items, empty_middle
                if not page["items"]:
                    empty_middle += 1
                assert page["next_cursor"] and page["next_cursor"] != cursor
                cursor = page["next_cursor"]
            raise AssertionError("Worklist paging loop")

        login(fixture["account"])
        tenant, actor = database.execute(
            "SELECT tenant_id,id FROM business.users WHERE account=%s",
            (fixture["account"],),
        ).fetchone()
        inventory = call("GET", "/source-inventory?limit=50")
        known = next(
            item for item in inventory["items"] if item["reconciliation"] == "current"
        )
        unknown = [
            item
            for item in inventory["items"]
            if item["association"] == "unknown_version"
        ]
        for item in (known, unknown[0]):
            path = "/source-investigations/" + item["object_ref"]
            if call("GET", path)["revision"] == 0:
                call(
                    "PUT",
                    path,
                    {
                        "selection_token": item["investigation_token"],
                        "expected_revision": 0,
                        "status": "investigating",
                        "note": "PRIVATE_WORKLIST_ACCEPTANCE_NOTE",
                        "confirm": True,
                    },
                )
        # Pagination-only metadata in this new synthetic tenant: no corresponding S3 objects.
        database.execute(
            """INSERT INTO business.source_investigations
          (id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,created_at)
          SELECT gen_random_uuid(),%s,md5('worklist-public-'||n)||md5('synthetic-public-'||n),
          'synthetic-store',%s||n,CASE n%%3 WHEN 0 THEN 'open' WHEN 1 THEN 'investigating' ELSE 'closed' END,
          'PRIVATE_WORKLIST_PAGINATION_NOTE',1,%s,%s,'2020-01-01T00:00:00Z'::timestamptz FROM generate_series(1,111)n""",
            (tenant, "knowledge/" + str(tenant) + "/misc/worklist-", actor, actor),
        )
        snapshot = database.execute(
            "SELECT id,object_ref,status,note,revision,updated_at FROM business.source_investigations WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        ledger = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        audit_before = database.execute(
            "SELECT count(*) FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_list'",
            (tenant,),
        ).fetchone()[0]
        expected = database.execute(
            "SELECT object_ref FROM business.source_investigations WHERE tenant_id=%s ORDER BY created_at DESC,id DESC",
            (tenant,),
        ).fetchall()
        items, _ = all_pages()
        assert [item["object_ref"] for item in items] == [row[0] for row in expected]
        assert len(items) == 113 and len({item["object_ref"] for item in items}) == 113
        assert len(call("GET", "/source-investigations")["items"]) == 20
        checks.append(
            {
                "name": "113 investigations paginate beyond legacy limits with equal timestamps and UUID tie breaker; list returns metadata only",
                "passed": True,
            }
        )

        counts = {}
        for status in ("open", "investigating", "closed"):
            filtered, _ = all_pages(status=status)
            counts[status] = database.execute(
                "SELECT count(*) FROM business.source_investigations WHERE tenant_id=%s AND status=%s",
                (tenant, status),
            ).fetchone()[0]
            assert len(filtered) == counts[status] and all(
                item["status"] == status for item in filtered
            )
        first = call("GET", "/source-investigations?limit=1")
        cursor = first["next_cursor"]
        call(
            "GET", "/source-investigations?status=closed&cursor=" + cursor, expected=400
        )
        call("GET", "/source-investigations?cursor=bad", expected=400)
        call("GET", "/source-investigations?limit=51", expected=400)
        call("GET", "/source-investigations?status=owned", expected=400)
        login("demo.admin")
        call("GET", "/source-investigations?cursor=" + cursor, expected=400)
        foreign = call("GET", "/source-investigations")
        assert all(
            item["object_ref"] not in {row[0] for row in expected}
            for item in foreign["items"]
        )
        # Account for only the target tenant's successful page audits below.
        successes -= 1
        login("demo.operator")
        call("GET", "/source-investigations", expected=403)
        login(fixture["account"])
        checks.append(
            {
                "name": "all status filters match tenant SQL counts; invalid status/limit/cursor, cross-actor cursor and operator access rejected; other tenant sees no target rows",
                "passed": True,
            }
        )

        base = database.execute(
            "SELECT knowledge_base_id FROM business.documents WHERE tenant_id=%s AND id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()[0]
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base),
            )
            visible, empty = all_pages(limit=1)
            assert empty >= 1 and len(visible) == 112
            assert known["object_ref"] not in {item["object_ref"] for item in visible}
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["admin", "operator"]), tenant, base),
            )
        checks.append(
            {
                "name": "known-resource ACL revocation hides its metadata, including an empty middle page; encrypted cursor continues without omission; fixture ACL restored",
                "passed": True,
            }
        )

        assert (
            database.execute(
                "SELECT id,object_ref,status,note,revision,updated_at FROM business.source_investigations WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == snapshot
        )
        assert (
            database.execute(
                "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == ledger
        )
        audits = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_list'",
            (tenant,),
        ).fetchall()
        assert len(audits) == audit_before + successes
        assert all(
            value not in json.dumps(audits)
            for value in (
                "PRIVATE_WORKLIST",
                "knowledge/",
                known["investigation_token"],
            )
        )
        checks.append(
            {
                "name": "successful target-tenant pages audited exactly once; failed requests unaudited; latest notes/revisions/timestamps and source ledger remain unchanged",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        **fixture,
                        "total": 113,
                        "status_counts": counts,
                        "object_ref": unknown[1]["object_ref"],
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
                "stage": "M7.16",
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
        f"Investigation worklist public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
