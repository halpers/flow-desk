"""Verify lossless history tier archival against an isolated mock stack and synthetic inventory tenant."""

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
            if path.endswith(("/restore", "/archive-metadata")):
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

        def archive(target, revision, expected, key=None, status=200, code=None):
            return call(
                "POST",
                target + f"/history/{revision}/archive-metadata",
                {"expected_revision": expected, "confirm": True},
                expected=status,
                key=key,
                code=code,
            )

        edit(path, 0, "PRIVATE_ARCHIVE_1", "closed", item["investigation_token"])
        # Test-only synthetic old edits use the real capture trigger, never update immutable snapshots.
        for revision in range(2, 113):
            database.execute(
                "UPDATE business.source_investigations SET revision=%s,note=%s,status=%s,updated_at='2020-01-01T00:00:00Z'::timestamptz + %s * interval '1 minute' WHERE tenant_id=%s AND object_ref=%s",
                (
                    revision,
                    f"PRIVATE_ARCHIVE_{revision} <script>literal</script>",
                    "investigating" if revision == 2 else "closed",
                    revision,
                    tenant,
                    ref,
                ),
            )
        current = edit(path, 112, "PRIVATE_ARCHIVE_113")
        investigation = database.execute(
            "SELECT id FROM business.source_investigations WHERE tenant_id=%s AND object_ref=%s",
            (tenant, ref),
        ).fetchone()[0]
        original = database.execute(
            "SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE investigation_id=%s ORDER BY revision",
            (investigation,),
        ).fetchall()
        first = call("GET", path + "/history?limit=9")
        key = str(uuid4())
        receipt = archive(path, 2, 113, key)
        archived = database.execute(
            "SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revision_archives WHERE investigation_id=%s AND revision=2",
            (investigation,),
        ).fetchone()
        assert archived == original[1]
        assert call("GET", path) == current
        assert (
            database.execute(
                "SELECT count(*) FROM business.source_investigation_revisions WHERE investigation_id=%s",
                (investigation,),
            ).fetchone()[0]
            == 112
        )
        archive(
            path, 1, 113, status=409, code="INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
        )
        for revision in (111, 112, 113):
            archive(
                path,
                revision,
                113,
                status=409,
                code="INVESTIGATION_HISTORY_ARCHIVE_PROTECTED",
            )
        archive(path, 2, 113, status=409, code="INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
        archive(path, 3, 112, status=409, code="INVESTIGATION_CHANGED")
        call(
            "POST",
            path + "/history/3/archive-metadata",
            {"expected_revision": 113, "confirm": False},
            expected=400,
        )
        call(
            "POST",
            path + "/history/2/archive-metadata",
            {"expected_revision": 112, "confirm": True},
            expected=409,
            key=key,
            code="IDEMPOTENCY_CONFLICT",
        )
        checks.append(
            {
                "name": "complete snapshot moved atomically; current record unchanged; age/latest-three/confirmation/CAS and parameter-bound key protections enforced",
                "passed": True,
            }
        )
        archive(path, 20, 113)
        archive(path, 105, 113)
        revisions = []
        page = first
        for _ in range(20):
            assert (
                page["snapshot_revision"] == 113
                and page["first_recorded_revision"] == 1
            )
            revisions.extend(item["revision"] for item in page["items"])
            if not page["has_more"]:
                break
            page = call("GET", path + "/history?limit=9&cursor=" + page["next_cursor"])
        assert revisions == list(range(113, 0, -1))
        restored = call(
            "POST",
            path + "/history/2/restore",
            {"expected_revision": 113, "confirm": True},
        )
        assert (
            restored["investigation"]["revision"] == 114
            and restored["investigation"]["note"] == original[1][5]
            and restored["investigation"]["status"] == "investigating"
        )
        archive(
            path, 3, 114, status=409, code="INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
        )
        edit(path, 114, "PRIVATE_ARCHIVE_LATER_EDIT")
        assert archive(path, 2, 113, key) == receipt
        assert call("GET", path)["revision"] == 115
        checks.append(
            {
                "name": "113 revisions remain pageable without duplication across tier moves; cold restore creates a new revision; old archive receipt cannot overwrite later edits",
                "passed": True,
            }
        )
        login("demo.admin")
        archive(path, 3, 115, status=404)
        login("demo.operator")
        archive(path, 3, 115, status=403)
        login(fixture["account"])
        known = next(
            item for item in inventory["items"] if item["reconciliation"] == "current"
        )
        known_path = "/source-investigations/" + known["object_ref"]
        known_current = call("GET", known_path)
        if known_current["revision"] == 0:
            known_current = edit(
                known_path,
                0,
                "Known synthetic original",
                token=known["investigation_token"],
            )
        prior = known_current["revision"]
        for revision in range(prior + 1, prior + 5):
            database.execute(
                "UPDATE business.source_investigations SET revision=%s,note=%s,status='closed',updated_at='2020-01-01T00:00:00Z' WHERE tenant_id=%s AND object_ref=%s",
                (
                    revision,
                    f"PRIVATE_KNOWN_ARCHIVE_{revision}",
                    tenant,
                    known["object_ref"],
                ),
            )
        known_current = edit(known_path, prior + 4, "Known final closed")
        known_key = str(uuid4())
        known_revision = prior + 1
        known_expected = known_current["revision"]
        known_receipt = archive(known_path, known_revision, known_expected, known_key)
        base, roles = database.execute(
            "SELECT kb.id,kb.read_roles FROM business.knowledge_bases kb JOIN business.documents d ON d.knowledge_base_id=kb.id AND d.tenant_id=kb.tenant_id WHERE d.tenant_id=%s AND d.id=%s",
            (tenant, fixture["document_id"]),
        ).fetchone()
        try:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(["operator"]), tenant, base),
            )
            archive(known_path, known_revision, known_expected, known_key, status=404)
            archive(known_path, prior + 2, known_expected, status=404)
            call("GET", known_path + "/history", expected=404)
            call(
                "POST",
                known_path + f"/history/{known_revision}/restore",
                {"expected_revision": known_expected, "confirm": True},
                expected=404,
            )
        finally:
            database.execute(
                "UPDATE business.knowledge_bases SET read_roles=%s::jsonb,acl_revision=acl_revision+1 WHERE tenant_id=%s AND id=%s",
                (json.dumps(roles), tenant, base),
            )
        assert (
            archive(known_path, known_revision, known_expected, known_key)
            == known_receipt
        )
        checks.append(
            {
                "name": "tenant and built-in-admin boundaries plus current resource ACL protect new archives, old receipts, cold history and cold restores",
                "passed": True,
            }
        )
        combined = database.execute(
            "SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revisions WHERE investigation_id=%s AND revision<=113 UNION ALL SELECT id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline FROM business.source_investigation_revision_archives WHERE investigation_id=%s ORDER BY revision",
            (investigation, investigation),
        ).fetchall()
        assert combined == original
        audits = database.execute(
            "SELECT summary FROM business.audit_events WHERE tenant_id=%s AND operation='knowledge.source_investigation_archive'",
            (tenant,),
        ).fetchall()
        receipts = database.execute(
            "SELECT count(*) FROM business.api_idempotency WHERE tenant_id=%s AND operation='knowledge.source_investigation_archive'",
            (tenant,),
        ).fetchone()[0]
        assert len(audits) == receipts == 4
        assert all(
            value not in json.dumps(audits)
            for value in (
                "PRIVATE_ARCHIVE",
                ref,
                "knowledge/",
                item["investigation_token"],
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
        checks.append(
            {
                "name": "both tiers retain exact original identity/content/actor/time; one immutable audit/receipt per archive; storage and source ledgers unchanged",
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
                        "revision": 115,
                        "target_revision": 104,
                        "source_note": "PRIVATE_ARCHIVE_104 <script>literal</script>",
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
                "stage": "M7.19",
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
        f"Investigation history archive public-API acceptance: {len(checks)}/{len(checks)} passed"
    )


if __name__ == "__main__":
    main()
