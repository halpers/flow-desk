"""Public API acceptance for a disposable mock stack with the S3 overlay."""

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
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text(encoding="utf-8").splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings.get("MODEL_PROVIDER", "mock") == "mock", (
        "Use a disposable mock stack"
    )
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    original = (ROOT / "flow-desk-agent/tests/fixtures/policy.pdf").read_bytes()
    digest = hashlib.sha256(original).hexdigest()
    checks = []
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=15,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

        def call(method, path, body=None, expected=200, key=None):
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
                "name": "Synthetic source " + str(uuid4()),
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        body = {
            "title": "Synthetic source PDF",
            "filename": "policy.pdf",
            "content_base64": base64.b64encode(original).decode(),
        }
        key = str(uuid4())
        version = call(
            "POST", f"/knowledge-bases/{base['id']}/documents", body, 202, key
        )
        assert (
            call("POST", f"/knowledge-bases/{base['id']}/documents", body, 202, key)[
                "id"
            ]
            == version["id"]
        )
        prefix = f"/documents/{version['document_id']}/versions/{version['id']}"

        def wait_archived():
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                rows = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ]
                current = next(row for row in rows if row["id"] == version["id"])
                if (
                    current["source_storage"] == "s3"
                    and current["index_status"] == "ready"
                ):
                    assert current["source_size"] == len(original)
                    assert not any(
                        part in current
                        for part in ("source_key", "source_store_id", "bucket")
                    )
                    return
                time.sleep(0.3)
            raise AssertionError("Archive/index did not finish")

        def download():
            data = call("GET", prefix + "/source")
            assert data["content_hash"] == digest
            assert base64.b64decode(data["content_base64"]) == original

        wait_archived()
        download()
        checks.append(
            "real S3 archival, immutable upload replay, index ready and exact original download"
        )
        call(
            "GET", f"/documents/{uuid4()}/versions/{version['id']}/source", expected=404
        )
        login("demo.operator")
        call("GET", prefix + "/source", expected=403)
        call("POST", prefix + "/restore-source", {}, 403)
        login("demo.admin")
        restore_key = str(uuid4())
        assert (
            call("POST", prefix + "/restore-source", {}, key=restore_key)[
                "source_storage"
            ]
            == "postgres"
        )
        assert (
            call("POST", prefix + "/restore-source", {}, key=restore_key)[
                "source_storage"
            ]
            == "postgres"
        )
        download()
        checks.append(
            "admin-only download/restore, document binding and idempotent restore to PostgreSQL"
        )
        archive_key = str(uuid4())
        call("POST", prefix + "/archive", {}, 202, archive_key)
        call("POST", prefix + "/archive", {}, 202, archive_key)
        wait_archived()
        download()
        checks.append("explicit new archive generation reuses the verified object")
    report = {
        "phase": "M7.4",
        "created_at": datetime.now(UTC).isoformat(),
        "model": "mock",
        "storage": "S3-compatible",
        "passed": len(checks),
        "checks": checks,
    }
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps({"phase": "M7.4", "passed": len(checks)}))


if __name__ == "__main__":
    main()
