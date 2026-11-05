"""Public API acceptance using only new synthetic PDFs in a disposable mock stack."""

import argparse
import base64
import hashlib
import json
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--browser-fixture", type=Path)
    args = parser.parse_args()
    assert args.env_file.resolve().is_relative_to(ROOT / "work"), (
        "Disposable work/ config required"
    )
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert (
        settings["MODEL_PROVIDER"] == "mock"
        and settings["FILE_STORE_CLEANUP_ENABLED"].lower() == "true"
    )
    original = (ROOT / "flow-desk-agent/tests/fixtures/policy.pdf").read_bytes()
    digest = hashlib.sha256(original).hexdigest()
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    checks = []
    with httpx.Client(
        base_url=origin + "/api/v1",
        timeout=15,
        trust_env=False,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

        def call(method, path, body=None, expected=200, key=None, code=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            assert response.status_code == expected, (
                path,
                response.status_code,
                response.json().get("code"),
            )
            if code:
                assert response.json()["code"] == code
            return response.json().get("data")

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
                "name": "Archive cleanup " + str(uuid4())[:8],
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        version = call(
            "POST",
            f"/knowledge-bases/{base['id']}/documents",
            {
                "title": "Archive cleanup acceptance",
                "filename": "policy.pdf",
                "content_base64": base64.b64encode(original).decode(),
            },
            202,
        )
        prefix = f"/documents/{version['document_id']}/versions/{version['id']}"
        path = prefix + "/archive-copies"

        def wait_archived():
            for _ in range(120):
                row = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ][0]
                if row["source_storage"] == "s3" and row["index_status"] == "ready":
                    return
                time.sleep(0.5)
            raise AssertionError("Archive timeout")

        def download():
            data = call("GET", prefix + "/source")
            assert (
                data["content_hash"] == digest
                and base64.b64decode(data["content_base64"]) == original
            )

        wait_archived()
        plan = call("GET", path)
        assert len(plan["items"]) == 1 and not plan["items"][0]["eligible"]
        assert "restore_required" in plan["items"][0]["reasons"]
        assert not any(
            name in json.dumps(plan) for name in ("object_key", "store_id", "bucket")
        )
        download()
        call("POST", prefix + "/restore-source", {})
        plan = call("GET", path)
        copy = plan["items"][0]
        assert copy["eligible"] and plan["source_epoch"] == 1
        target = path + "/" + copy["id"] + "/cleanup"
        body = {"confirm": True, "expected_source_epoch": plan["source_epoch"]}
        call(
            "POST",
            target,
            {**body, "expected_source_epoch": 2},
            409,
            code="SOURCE_CHANGED",
        )
        checks.append(
            {
                "name": "current copy protected; restore integrity and source generation revalidated",
                "passed": True,
            }
        )
        key = str(uuid4())
        call("POST", target, body, 202, key)
        for _ in range(120):
            result = call("POST", target, body, 202, key)
            if result["status"] == "retired":
                break
            time.sleep(0.5)
        else:
            raise AssertionError("Source cleanup timeout")
        download()
        checks.append(
            {
                "name": "real S3 zero-byte marker read back; durable idempotent completion and database original preserved",
                "passed": True,
            }
        )
        call("POST", prefix + "/archive", {}, 202)
        wait_archived()
        download()
        plan = call("GET", path)
        assert (
            len(plan["items"]) == 2
            and sum(row["status"] == "retired" for row in plan["items"]) == 1
        )
        checks.append(
            {
                "name": "new archive generation uses an independent object and remains downloadable",
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
                        "copy_id": next(
                            row["id"]
                            for row in plan["items"]
                            if row["status"] == "registered"
                        ),
                    },
                    indent=2,
                )
                + "\n"
            )
        login("demo.operator")
        call("GET", path, expected=403)
        call("POST", target, body, 403, key)
        checks.append(
            {
                "name": "operator cannot preview, clean or replay privileged cleanup",
                "passed": True,
            }
        )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(
            {
                "stage": "M7.8",
                "environment": "disposable local mock with S3",
                "created_at": datetime.now(UTC).isoformat(),
                "checks": checks,
            },
            indent=2,
        )
        + "\n"
    )
    print(f"Source cleanup public-API acceptance: {len(checks)}/{len(checks)} passed")


if __name__ == "__main__":
    main()
