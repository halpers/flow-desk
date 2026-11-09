"""Check immutable investigation history using a new synthetic object in a disposable mock stack."""

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
        successful_pages = 0

        def call(method, path, body=None, expected=200, key=None):
            nonlocal successful_pages
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
            if path.split("?")[0].endswith("/history"):
                assert response.headers.get("cache-control") == "no-store"
                if expected == 200:
                    successful_pages += 1
                    assert all(
                        value not in json.dumps(envelope)
                        for value in (
                            '"object_key"',
                            '"store_id"',
                            '"investigation_id"',
                            '"selection_token"',
                            "knowledge/",
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

        login(fixture["account"])
        tenant, actor = database.execute(
            "SELECT tenant_id,id FROM business.users WHERE account=%s",
            (fixture["account"],),
        ).fetchone()
        inventory = call("GET", "/source-inventory?limit=50")
        unknown = [
            item
            for item in inventory["items"]
            if item["association"] == "unknown_version"
        ]
        item = next(
            item
            for item in unknown
            if call("GET", "/source-investigations/" + item["object_ref"])["revision"]
            == 0
        )
        ref = item["object_ref"]
        path = "/source-investigations/" + ref
        history = path + "/history"
        ledger = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        audit_before = database.execute(
            "SELECT count(*) FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_history'",
            (tenant,),
        ).fetchone()[0]
        first_body = {
            "selection_token": item["investigation_token"],
            "expected_revision": 0,
            "status": "open",
            "note": "PRIVATE_HISTORY_REVISION_1 <script>literal</script>",
            "confirm": True,
        }
        first_key = str(uuid4())
        first = call("PUT", path, first_body, key=first_key)

        def save(revision):
            return call(
                "PUT",
                path,
                {
                    "selection_token": "",
                    "expected_revision": revision - 1,
                    "status": "closed" if revision % 2 == 0 else "investigating",
                    "note": f"PRIVATE_HISTORY_REVISION_{revision}",
                    "confirm": True,
                },
            )

        for revision in range(2, 114):
            save(revision)
        page = call("GET", history + "?limit=11")
        assert page["snapshot_revision"] == 113 and page["first_recorded_revision"] == 1
        save(114)
        items = []
        for _ in range(150):
            assert page["snapshot_revision"] == 113
            items.extend(page["items"])
            if not page["has_more"]:
                assert not page["next_cursor"]
                break
            page = call("GET", history + "?limit=11&cursor=" + page["next_cursor"])
            assert page["current_revision"] == 114
        else:
            raise AssertionError("History paging loop")
        assert [item["revision"] for item in items] == list(range(113, 0, -1))
        assert items[-1]["note"] == first_body["note"]
        assert items[-1]["changed_at"] == first["updated_at"]
        assert all(
            item["status"]
            == (
                "open"
                if item["revision"] == 1
                else "closed"
                if item["revision"] % 2 == 0
                else "investigating"
            )
            for item in items
        )
        assert all(
            not item["is_baseline"]
            and item["changed_by"] == str(actor)
            and item["changed_at"]
            for item in items
        )
        assert all(
            item["note"] == f"PRIVATE_HISTORY_REVISION_{item['revision']}"
            for item in items[:-1]
        )
        checks.append(
            {
                "name": "113 actual saves paginate by immutable revision; a later save preserves the original upper bound; each snapshot retains note/status/actor/time",
                "passed": True,
            }
        )

        assert call("PUT", path, first_body, key=first_key) == first
        fresh = call("GET", history)
        assert (
            fresh["current_revision"] == 114
            and fresh["snapshot_revision"] == 114
            and len(fresh["items"]) == 20
            and fresh["items"][0]["revision"] == 114
        )
        investigation = database.execute(
            "SELECT id FROM business.source_investigations WHERE tenant_id=%s AND object_ref=%s",
            (tenant, ref),
        ).fetchone()[0]
        assert (
            database.execute(
                "SELECT count(*) FROM business.source_investigation_revisions WHERE tenant_id=%s AND investigation_id=%s",
                (tenant, investigation),
            ).fetchone()[0]
            == 114
        )
        checks.append(
            {
                "name": "refresh exposes the new revision; replay returns the original receipt and adds no snapshot; all 114 saves have exactly one history row",
                "passed": True,
            }
        )

        cursor = fresh["next_cursor"]
        call("GET", history + "?limit=51", expected=400)
        call("GET", history + "?cursor=bad", expected=400)
        known = next(
            item for item in inventory["items"] if item["reconciliation"] == "current"
        )
        known_path = "/source-investigations/" + known["object_ref"]
        current = call("GET", known_path)
        if current["revision"] == 0:
            call(
                "PUT",
                known_path,
                {**first_body, "selection_token": known["investigation_token"]},
            )
        call("GET", known_path + "/history?cursor=" + cursor, expected=400)
        login("demo.admin")
        call("GET", history, expected=404)
        login("demo.operator")
        call("GET", history, expected=403)
        login(fixture["account"])
        base = database.execute(
            "SELECT knowledge_base_id FROM business.documents WHERE tenant_id=%s AND id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()[0]
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base),
            )
            call("GET", known_path + "/history", expected=404)
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["admin", "operator"]), tenant, base),
            )
        checks.append(
            {
                "name": "invalid inputs, cross-object cursors, foreign tenants and ordinary users denied; known-resource ACL revocation blocks history and is restored",
                "passed": True,
            }
        )

        notes_before = database.execute(
            "SELECT revision,note,status,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE tenant_id=%s AND investigation_id=%s ORDER BY revision",
            (tenant, investigation),
        ).fetchall()
        call("GET", history + "?limit=50")
        assert (
            database.execute(
                "SELECT revision,note,status,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE tenant_id=%s AND investigation_id=%s ORDER BY revision",
                (tenant, investigation),
            ).fetchall()
            == notes_before
        )
        audits = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_history'",
            (tenant,),
        ).fetchall()
        assert len(audits) == audit_before + successful_pages
        assert all(
            value not in json.dumps(audits)
            for value in (
                "PRIVATE_HISTORY",
                first_body["note"],
                item["investigation_token"],
                "knowledge/",
            )
        )
        assert (
            database.execute(
                "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == ledger
        )
        checks.append(
            {
                "name": "history reads preserve snapshots and source ledger; successful pages audited exactly once without notes/tokens/locators, rejected reads unaudited",
                "passed": True,
            }
        )
        if args.browser_fixture:
            args.browser_fixture.write_text(
                json.dumps({**fixture, "object_ref": ref, "revision": 114}, indent=2)
                + "\n",
                encoding="utf-8",
            )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.17",
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
        f"Investigation history public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
