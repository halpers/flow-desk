"""Verify atomic previewed investigation history batch archival against an isolated mock stack and synthetic inventory tenant."""

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
            if path.endswith(("/restore", "/archive-batch", "/archive-preview")):
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
        tenant, _actor = database.execute(
            "SELECT tenant_id,id FROM business.users WHERE account=%s",
            (fixture["account"],),
        ).fetchone()

        ref = fixture["object_ref"]
        path = "/source-investigations/" + ref
        current = call("GET", path)
        expected = current["revision"]
        assert expected == 115
        ledger = database.execute(
            "SELECT id,status,object_key FROM business.source_objects WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        sources = database.execute(
            "SELECT id,source_epoch,source_storage,content_hash,md5(source_bytes) FROM business.document_versions WHERE tenant_id=%s ORDER BY id",
            (tenant,),
        ).fetchall()
        investigation = database.execute(
            "SELECT id FROM business.source_investigations WHERE tenant_id=%s AND object_ref=%s",
            (tenant, ref),
        ).fetchone()[0]

        def snapshots():
            return database.execute(
                "SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE investigation_id=%s AND revision<=115 UNION ALL SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revision_archives WHERE investigation_id=%s AND revision<=115 ORDER BY revision",
                (investigation, investigation),
            ).fetchall()

        def cold_count():
            return database.execute(
                "SELECT count(*) FROM business.source_investigation_revision_archives WHERE investigation_id=%s",
                (investigation,),
            ).fetchone()[0]

        def edit(target, revision, note, status="closed"):
            return call(
                "PUT",
                target,
                {
                    "expected_revision": revision,
                    "selection_token": "",
                    "note": note,
                    "status": status,
                    "confirm": True,
                },
            )

        def preview(target, revisions, revision, status=200, code=None):
            return call(
                "POST",
                target + "/history/archive-preview",
                {"expected_revision": revision, "revisions": revisions},
                expected=status,
                code=code,
            )

        def batch(
            target, revisions, revision, key=None, status=200, code=None, confirm=True
        ):
            return call(
                "POST",
                target + "/history/archive-batch",
                {
                    "expected_revision": revision,
                    "revisions": revisions,
                    "confirm": confirm,
                },
                expected=status,
                key=key,
                code=code,
            )

        originals = snapshots()
        cold_before = cold_count()
        selected = [101, 102]
        observed = preview(path, selected, expected)
        assert (
            observed["maximum_items"] == 50 and observed["current_revision"] == expected
        )
        assert [item["revision"] for item in observed["items"]] == selected
        assert [item["note"] for item in observed["items"]] == [
            originals[100][5],
            originals[101][5],
        ]
        assert cold_count() == cold_before and call("GET", path) == current
        key = str(uuid4())
        receipt = batch(path, selected, expected, key)
        assert [item["revision"] for item in receipt["items"]] == selected
        assert cold_count() == cold_before + 2 and snapshots() == originals
        assert call("GET", path) == current
        edit(path, 115, "PRIVATE_BATCH_LATER_EDIT")
        assert batch(path, selected, expected, key) == receipt
        assert call("GET", path)["revision"] == 116
        checks.append(
            {
                "name": "preview returns exact immutable selection without archival; one atomic batch preserves all original snapshots/current state; original receipt survives later edit",
                "passed": True,
            }
        )
        for selection in ([], [100, 100], [102, 100], [0, 100], list(range(1, 52))):
            preview(path, selection, 116, status=400)
            batch(path, selection, 116, status=400)
        batch(path, [100, 103], 116, status=400, confirm=False)
        batch(path, [100, 103], 115, status=409, code="INVESTIGATION_CHANGED")
        batch(
            path,
            [100, 116],
            116,
            status=409,
            code="INVESTIGATION_HISTORY_ARCHIVE_PROTECTED",
        )
        batch(path, [100, 999], 116, status=404)
        batch(
            path,
            [100, 101],
            116,
            status=409,
            code="INVESTIGATION_HISTORY_ALREADY_ARCHIVED",
        )
        batch(path, [101, 103], 115, key, status=409, code="IDEMPOTENCY_CONFLICT")
        edit(path, 116, "PRIVATE_BATCH_ACTIVE", "investigating")
        batch(
            path,
            [100, 103],
            117,
            status=409,
            code="INVESTIGATION_HISTORY_ARCHIVE_PROTECTED",
        )
        edit(path, 117, "PRIVATE_BATCH_FINAL_CLOSED")
        assert cold_count() == cold_before + 2 and snapshots() == originals
        login("demo.admin")
        preview(path, [100, 103], 118, status=404)
        batch(path, [100, 103], 118, status=404)
        login("demo.operator")
        preview(path, [100, 103], 118, status=403)
        batch(path, [100, 103], 118, status=403)
        login(fixture["account"])
        checks.append(
            {
                "name": "bounds, strict selection order, duplicates, confirmation, stale revision and mixed protected/missing/archived selections reject entire batch; tenant/admin boundaries enforced",
                "passed": True,
            }
        )
        page = call("GET", path + "/history?limit=13")
        revisions = []
        for _ in range(12):
            revisions.extend(item["revision"] for item in page["items"])
            if not page["has_more"]:
                break
            page = call("GET", path + "/history?limit=13&cursor=" + page["next_cursor"])
        assert revisions == list(range(118, 0, -1))
        inventory = call("GET", "/source-inventory?limit=50")
        known = next(
            item for item in inventory["items"] if item["reconciliation"] == "current"
        )
        known_path = "/source-investigations/" + known["object_ref"]
        prior = call("GET", known_path)["revision"]
        assert prior > 0
        for revision in range(prior + 1, prior + 5):
            database.execute(
                "UPDATE business.source_investigations SET revision=%s,note=%s,status='closed',updated_at='2020-01-01T00:00:00Z' WHERE tenant_id=%s AND object_ref=%s",
                (
                    revision,
                    f"PRIVATE_KNOWN_BATCH_{revision}",
                    tenant,
                    known["object_ref"],
                ),
            )
        known_current = edit(known_path, prior + 4, "Known closed for batch")
        known_selection = [prior + 1, prior + 2]
        known_revision = known_current["revision"]
        known_key = str(uuid4())
        preview(known_path, known_selection, known_revision)
        known_receipt = batch(known_path, known_selection, known_revision, known_key)
        base, roles = database.execute(
            "SELECT kb.id,kb.read_roles FROM business.knowledge_bases kb JOIN business.documents d ON d.knowledge_base_id=kb.id AND d.tenant_id=kb.tenant_id WHERE d.tenant_id=%s AND d.id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base),
            )
            preview(known_path, [prior + 3], known_revision, status=404)
            batch(known_path, [prior + 3], known_revision, status=404)
            batch(known_path, known_selection, known_revision, known_key, status=404)
            call(
                "POST",
                known_path + f"/history/{prior + 1}/restore",
                {"expected_revision": known_revision, "confirm": True},
                expected=404,
            )
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(roles), tenant, base),
            )
        assert (
            batch(known_path, known_selection, known_revision, known_key)
            == known_receipt
        )
        checks.append(
            {
                "name": "history remains complete after batches; current resource ACL protects preview/new batch/old receipt/cold restore; restored access recovers original receipt",
                "passed": True,
            }
        )
        audits = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_archive_batch'",
            (tenant,),
        ).fetchall()
        count = database.execute(
            "SELECT count(*) FROM business.api_idempotency WHERE tenant_id=%s AND operation='knowledge.source_investigation_archive_batch'",
            (tenant,),
        ).fetchone()[0]
        assert len(audits) == count == 2
        assert all(
            value not in json.dumps(audits)
            for value in ("PRIVATE_BATCH", ref, "knowledge/")
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
        assert snapshots() == originals
        checks.append(
            {
                "name": "one safe immutable audit/receipt per committed batch; all original identities/content/actor/time and storage/source ledgers unchanged",
                "passed": True,
            }
        )
        if args.browser_fixture:
            assert args.browser_fixture.resolve().is_relative_to(ROOT / "work")
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        **fixture,
                        "revision": 118,
                        "target_revisions": [106, 107],
                        "source_notes": [
                            "PRIVATE_ARCHIVE_106 <script>literal</script>",
                            "PRIVATE_ARCHIVE_107 <script>literal</script>",
                        ],
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
                "stage": "M7.20",
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
        f"Investigation history batch public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
