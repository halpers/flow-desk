"""Construct only new synthetic PDFs in a disposable work/ mock S3 stack."""

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
    assert all(
        settings[name].lower() == "true"
        for name in (
            "FILE_STORE_ADOPTION_ENABLED",
            "FILE_STORE_INVENTORY_ENABLED",
            "FILE_STORE_CLEANUP_ENABLED",
        )
    )
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
                "name": "Source adoption " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            201,
        )

        def fixture(title):
            version = call(
                "POST",
                f"/knowledge-bases/{base['id']}/documents",
                {
                    "title": title,
                    "filename": "policy.pdf",
                    "content_base64": base64.b64encode(original).decode(),
                },
                202,
            )
            prefix = f"/documents/{version['document_id']}/versions/{version['id']}"
            for _ in range(120):
                row = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ][0]
                if row["source_storage"] == "s3" and row["index_status"] == "ready":
                    break
                time.sleep(0.25)
            else:
                raise AssertionError("Archive timeout")
            # Simulates pre-ledger state ONLY for this newly created version.
            removed = database.execute(
                "DELETE FROM business.source_objects WHERE version_id=%s AND source_epoch=0 "
                "AND status='registered' RETURNING id",
                (version["id"],),
            ).fetchall()
            assert len(removed) == 1
            return version, prefix

        version, prefix = fixture("Source adoption acceptance")
        inventory = prefix + "/source-inventory"
        path = prefix + "/source-adoptions"
        page = call("GET", inventory)
        assert page["adoption_enabled"] and not page["items"][0]["adoption_eligible"]
        assert page["items"][0]["adoption_reason"] == "restore_required"
        call("POST", prefix + "/restore-source", {})
        page = call("GET", inventory)
        item = page["items"][0]
        assert (
            item["adoption_eligible"]
            and item["adoption_token"]
            and not page["content_verified"]
        )
        body = {
            "object_ref": item["object_ref"],
            "adoption_token": item["adoption_token"],
            "expected_source_epoch": page["source_epoch"],
            "confirm": True,
        }
        call("POST", path, {**body, "confirm": False}, 400)
        call("POST", path, {**body, "adoption_token": "tampered"}, 400)
        login("demo.operator")
        call("POST", path, body, 403)
        login("demo.admin")
        checks.append(
            {
                "name": "restored verified database original and explicit scoped selection required; ordinary operator and invalid confirmation rejected",
                "passed": True,
            }
        )

        key = str(uuid4())
        receipt = call("POST", path, body, key=key)
        replay = call("POST", path, body, key=key)
        assert (
            replay == receipt
            and receipt["source_epoch"] == 0
            and receipt["version_id"] == version["id"]
        )
        assert receipt["object_ref"] == item["object_ref"] and receipt["verified_at"]
        call("POST", path, body, 409, code="SOURCE_ALREADY_REGISTERED")
        call(
            "POST",
            path,
            {**body, "object_ref": "a" * 64},
            409,
            key=key,
            code="IDEMPOTENCY_CONFLICT",
        )
        data = call("GET", prefix + "/source")
        assert (
            data["content_hash"] == digest
            and base64.b64decode(data["content_base64"]) == original
        )
        after = call("GET", inventory)["items"][0]
        assert (
            after["registered_id"] == receipt["object_id"]
            and after["reconciliation"] == "registered"
        )
        assert (after["object_ref"], after["modified_at"], after["size"]) == (
            item["object_ref"],
            item["modified_at"],
            item["size"],
        )
        checks.append(
            {
                "name": "real S3 bytes verified and registered once; same-key immutable receipt; source and listed object metadata unchanged",
                "passed": True,
            }
        )

        plan = call("GET", prefix + "/archive-copies")
        assert len(plan["items"]) == 1 and plan["items"][0]["eligible"]
        cleanup_path = prefix + "/archive-copies/" + receipt["object_id"] + "/cleanup"
        cleanup_key = str(uuid4())
        cleanup_body = {"confirm": True, "expected_source_epoch": plan["source_epoch"]}
        for _ in range(120):
            result = call("POST", cleanup_path, cleanup_body, 202, key=cleanup_key)
            if result["status"] == "retired":
                break
            time.sleep(0.25)
        else:
            raise AssertionError("Cleanup timeout")
        assert call("POST", path, body, key=key) == receipt
        retired = call("GET", inventory)["items"][0]
        assert (
            retired["reconciliation"] == "retired_zero_bytes"
            and not retired["adoption_eligible"]
        )
        count = database.execute(
            "SELECT count(*) FROM business.audit_events WHERE operation='knowledge.source_adopt' AND object_id=%s",
            (receipt["object_id"],),
        ).fetchone()[0]
        assert count == 1
        data = call("GET", prefix + "/source")
        assert base64.b64decode(data["content_base64"]) == original
        checks.append(
            {
                "name": "adopted copy enters existing confirmed cleanup; retirement and replay preserve one adoption audit and original bytes",
                "passed": True,
            }
        )

        if args.browser_fixture:
            browser_version, _ = fixture("Source adoption browser")
            args.browser_fixture.write_text(
                json.dumps(
                    {
                        "base_name": base["name"],
                        "document_title": "Source adoption browser",
                        "document_id": browser_version["document_id"],
                        "version_id": browser_version["id"],
                    },
                    indent=2,
                )
                + "\n"
            )

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.12",
                "environment": "disposable local mock with S3",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Source adoption public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
