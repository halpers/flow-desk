"""Verify manual history restoration against an isolated mock stack and synthetic inventory tenant."""

import argparse
import json
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
import psycopg

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--inventory-fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--browser-fixture", type=Path)
    args = parser.parse_args()
    assert all(
        path.resolve().is_relative_to(ROOT / "work")
        for path in (args.env_file, args.inventory_fixture)
    )
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

        def call(method, path, body=None, expected=200, key=None, code=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            envelope = response.json()
            assert response.status_code == expected, (
                method,
                response.status_code,
                envelope.get("code"),
            )
            if code:
                assert envelope.get("code") == code
            if path.endswith("/restore"):
                assert response.headers.get("cache-control") == "no-store"
                assert all(
                    value not in json.dumps(envelope)
                    for value in (
                        '"store_id"',
                        '"object_key"',
                        '"selection_token"',
                        '"investigation_id"',
                        "knowledge/",
                    )
                )
            return envelope.get("data")

        def login(account):
            result = call(
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )
            http.headers["Authorization"] = "Bearer " + result["access_token"]

        login(fixture["account"])
        tenant, actor = database.execute(
            "SELECT tenant_id,id FROM business.users WHERE account=%s",
            (fixture["account"],),
        ).fetchone()
        inventory = call("GET", "/source-inventory?limit=50")
        item = next(
            item
            for item in inventory["items"]
            if item["association"] == "unknown_version"
            and call("GET", "/source-investigations/" + item["object_ref"])["revision"]
            == 0
        )
        ref = item["object_ref"]
        path = "/source-investigations/" + ref
        endpoint = path + "/history/1/restore"
        ledger = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        sources = database.execute(
            "SELECT id,source_epoch,source_storage,content_hash,md5(source_bytes) FROM business.document_versions WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()

        def edit(target, expected, note, status="closed", token=""):
            return call(
                "PUT",
                target,
                {
                    "expected_revision": expected,
                    "selection_token": token,
                    "note": note,
                    "status": status,
                    "confirm": True,
                },
            )

        first = edit(
            path,
            0,
            "PRIVATE_RESTORE_FIRST <script>literal</script>",
            "open",
            item["investigation_token"],
        )
        second = edit(path, 1, "PRIVATE_RESTORE_SECOND")
        investigation = database.execute(
            "SELECT id FROM business.source_investigations WHERE tenant_id=%s AND object_ref=%s",
            (tenant, ref),
        ).fetchone()[0]

        def snapshots():
            return database.execute(
                "SELECT revision,note,status,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE tenant_id=%s AND investigation_id=%s ORDER BY revision",
                (tenant, investigation),
            ).fetchall()

        before = snapshots()
        assert before[-1][1] == second["note"]
        body = {"expected_revision": 2, "confirm": True}
        key = str(uuid4())
        receipt = call("POST", endpoint, body, key=key)
        saved = receipt["investigation"]
        assert receipt["restored_from_revision"] == 1 and saved["revision"] == 3
        assert (
            saved["note"] == first["note"]
            and saved["status"] == first["status"]
            and saved["updated_by"] == str(actor)
        )
        assert (
            datetime.fromisoformat(saved["updated_at"]).timestamp()
            > datetime.now(UTC).timestamp() - 60
        )
        assert snapshots()[:2] == before and snapshots()[2][1:4] == (
            first["note"],
            first["status"],
            actor,
        )
        assert not snapshots()[2][-1]
        checks.append(
            {
                "name": "restore copies exact server history into a new revision, preserving old snapshots and using current actor/time",
                "passed": True,
            }
        )
        fourth = edit(path, 3, "PRIVATE_RESTORE_LATER_EDIT")
        assert call("POST", endpoint, body, key=key) == receipt
        assert call("GET", path) == fourth and len(snapshots()) == 4
        checks.append(
            {
                "name": "replaying a committed restore returns the original receipt after a later edit without another effect",
                "passed": True,
            }
        )
        call(
            "POST",
            endpoint,
            {"expected_revision": 4, "confirm": True},
            expected=409,
            key=key,
            code="IDEMPOTENCY_CONFLICT",
        )
        call("POST", endpoint, body, expected=409, code="INVESTIGATION_CHANGED")
        call("POST", endpoint, {"expected_revision": 4, "confirm": False}, expected=400)
        call(
            "POST",
            path + "/history/4/restore",
            {"expected_revision": 4, "confirm": True},
            expected=409,
            code="INVESTIGATION_RESTORE_NOT_OLDER",
        )
        call(
            "POST",
            path + "/history/0/restore",
            {"expected_revision": 4, "confirm": True},
            expected=400,
        )
        login("demo.admin")
        call("POST", endpoint, body, expected=404)
        login("demo.operator")
        call("POST", endpoint, body, expected=403)
        login(fixture["account"])
        checks.append(
            {
                "name": "explicit confirmation, revision CAS, source bounds, parameter-bound keys, tenant and built-in-admin boundaries enforced",
                "passed": True,
            }
        )
        known = next(
            item for item in inventory["items"] if item["reconciliation"] == "current"
        )
        known_path = "/source-investigations/" + known["object_ref"]
        current = call("GET", known_path)
        if current["revision"] == 0:
            current = edit(
                known_path,
                0,
                "Synthetic known original",
                "open",
                known["investigation_token"],
            )
        prior = current["revision"]
        changed = edit(known_path, prior, "Synthetic known replacement")
        known_endpoint = known_path + f"/history/{prior}/restore"
        known_key = str(uuid4())
        known_body = {"expected_revision": changed["revision"], "confirm": True}
        known_receipt = call("POST", known_endpoint, known_body, key=known_key)
        base, roles = database.execute(
            "SELECT kb.id,kb.read_roles FROM business.knowledge_bases kb JOIN business.documents d ON d.knowledge_base_id=kb.id AND d.tenant_id=kb.tenant_id WHERE d.tenant_id=%s AND d.id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base),
            )
            call("POST", known_endpoint, known_body, expected=404, key=known_key)
            call(
                "POST",
                known_endpoint,
                {
                    "expected_revision": known_receipt["investigation"]["revision"],
                    "confirm": True,
                },
                expected=404,
            )
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(roles), tenant, base),
            )
        assert call("POST", known_endpoint, known_body, key=known_key) == known_receipt
        audits = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_restore'",
            (tenant,),
        ).fetchall()
        receipts = database.execute(
            "SELECT count(*) FROM business.api_idempotency WHERE tenant_id=%s AND operation='knowledge.source_investigation_restore'",
            (tenant,),
        ).fetchone()[0]
        assert len(audits) == receipts == 2
        assert all(
            value not in json.dumps(audits)
            for value in (
                "PRIVATE_RESTORE",
                first["note"],
                ref,
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
        assert (
            database.execute(
                "SELECT id,source_epoch,source_storage,content_hash,md5(source_bytes) FROM business.document_versions WHERE tenant_id=%s ORDER BY id",
                (tenant,),
            ).fetchall()
            == sources
        )
        assert snapshots()[:2] == before and len(snapshots()) == 4
        checks.append(
            {
                "name": "current resource ACL blocks new restores and old receipts; restoring access recovers receipt; audits redacted and storage/source ledgers unchanged",
                "passed": True,
            }
        )
        if args.browser_fixture:
            assert args.browser_fixture.resolve().is_relative_to(ROOT / "work")
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        **fixture,
                        "object_ref": ref,
                        "revision": 4,
                        "source_note": first["note"],
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
                "stage": "M7.18",
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
        f"Investigation restore public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
