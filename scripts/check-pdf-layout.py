"""Fixed bilingual layout comparison and public API acceptance on a disposable mock stack."""

import argparse
import base64
import hashlib
import json
import time
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "flow-desk-agent/tests/fixtures"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--output", type=Path)
    parser.add_argument(
        "--require-s3",
        action="store_true",
        help="Require archived originals in an S3-enabled stack",
    )
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
    checks, comparison = [], []
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=15,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

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
                assert result.get("code") == code, result.get("code")
            return result.get("data")

        def login(account):
            token = call(
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )["access_token"]
            http.headers["Authorization"] = "Bearer " + token

        login("demo.admin")
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Synthetic layout " + str(uuid4()),
                "read_roles": ["admin", "operator"],
            },
            201,
        )

        def upload(filename, mode):
            raw = (FIXTURES / filename).read_bytes()
            body = {
                "title": "Synthetic " + mode + " " + filename,
                "filename": filename,
                "pdf_layout": mode,
                "content_base64": base64.b64encode(raw).decode(),
            }
            key = str(uuid4())
            path = f"/knowledge-bases/{base['id']}/documents"
            version = call("POST", path, body, 202, key)
            assert call("POST", path, body, 202, key)["id"] == version["id"]
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                rows = call("GET", f"/documents/{version['document_id']}/versions")[
                    "items"
                ]
                current = next(row for row in rows if row["id"] == version["id"])
                if current["index_status"] != "pending":
                    assert current["pdf_layout"] == mode
                    return current, raw
                time.sleep(0.3)
            raise AssertionError("Index did not finish")

        def pages(version):
            return call(
                "GET",
                f"/documents/{version['document_id']}/versions/{version['id']}/pages",
            )["pages"]

        for filename, mode in [
            ("layout-columns.pdf", "columns"),
            ("layout-table.pdf", "table"),
        ]:
            old, _ = upload(filename, "plain")
            selected, raw = upload(filename, mode)
            assert old["index_status"] == selected["index_status"] == "ready"
            before, after = pages(old), pages(selected)
            assert [page["page_number"] for page in after] == list(
                range(1, len(after) + 1)
            )
            for first, second in zip(before, after, strict=True):
                assert Counter("".join(first["text"].split())) == Counter(
                    "".join(second["text"].split())
                )
            if mode == "columns":
                baseline_ok = before[0]["text"].index("LEFT-5") < before[0][
                    "text"
                ].index("RIGHT-1")
                assert after[0]["text"].index("LEFT-5") < after[0]["text"].index(
                    "RIGHT-1"
                )
                assert after[1]["text"].index("左栏五") < after[1]["text"].index(
                    "右栏一"
                )
                assert "四十八个月" in after[1]["text"]
            else:
                baseline_ok = any(
                    "天琴专业设备" in row and "六十个月" in row
                    for row in before[0]["text"].splitlines()
                )
                row = next(
                    row
                    for row in after[0]["text"].splitlines()
                    if "天琴专业设备" in row
                )
                assert (
                    "六十个月" in row
                    and "设备编号与发票" in row
                    and "二十四个月" not in row
                )
            assert not baseline_ok
            comparison.append(
                {
                    "fixture": filename,
                    "sha256": hashlib.sha256(raw).hexdigest(),
                    "pages": len(after),
                    "plain_order_or_row_association": False,
                    "selected_order_or_row_association": True,
                    "non_whitespace_content_equal": True,
                }
            )
            if args.require_s3:
                prefix = (
                    f"/documents/{selected['document_id']}/versions/{selected['id']}"
                )
                deadline = time.monotonic() + 60
                while (
                    selected["source_storage"] != "s3" and time.monotonic() < deadline
                ):
                    time.sleep(0.3)
                    selected = next(
                        row
                        for row in call(
                            "GET", f"/documents/{selected['document_id']}/versions"
                        )["items"]
                        if row["id"] == selected["id"]
                    )
                assert selected["source_storage"] == "s3"
                assert (
                    base64.b64decode(call("GET", prefix + "/source")["content_base64"])
                    == raw
                )
            activation = {
                "version_id": selected["id"],
                "expected_version": 1,
                "confirm_ocr": True,
            }
            activate_path = f"/documents/{selected['document_id']}/activate"
            call(
                "POST",
                activate_path,
                activation,
                409,
                code="PDF_LAYOUT_REVIEW_REQUIRED",
            )
            activation["confirm_layout"] = True
            login("demo.operator")
            call("POST", activate_path, activation, 403)
            login("demo.admin")
            active = call("POST", activate_path, activation)
            try:
                if mode == "columns":
                    login("demo.operator")
                    conversation = call(
                        "POST",
                        "/conversations",
                        {"title": "Synthetic layout retrieval"},
                        201,
                    )
                    run = call(
                        "POST",
                        f"/conversations/{conversation['id']}/messages",
                        {
                            "client_message_id": str(uuid4()),
                            "content": "星河保修政策规定设备保修期限多久？",
                            "attachments": [],
                        },
                        202,
                    )
                    deadline = time.monotonic() + 45
                    while time.monotonic() < deadline:
                        snapshot = call("GET", "/runs/" + run["id"])
                        if snapshot["status"] not in ("queued", "running"):
                            break
                        time.sleep(0.2)
                    assert snapshot["status"] == "succeeded"
                    evidence = [
                        call("GET", "/citations/" + item["id"])
                        for item in snapshot["current_message"]["citations"]
                    ]
                    citation = next(
                        item
                        for item in evidence
                        if item["version_id"] == selected["id"]
                        and item["page_number"] == 2
                    )
                    assert "四十八个月" in citation["excerpt"]
                    citation_id = citation["id"]
            finally:
                login("demo.admin")
                call(
                    "POST",
                    f"/documents/{active['id']}/withdraw",
                    {"expected_version": active["version"]},
                )
            if mode == "columns":
                login("demo.operator")
                call(
                    "GET",
                    "/citations/" + citation_id,
                    expected=409,
                    code="EVIDENCE_REVOKED",
                )
                login("demo.admin")
            checks.append(
                mode
                + ": fixed comparison, immutable replay, reviewed activation and withdrawal"
            )
        for filename in ("layout-ambiguous.pdf", "layout-rotated.pdf"):
            failed, _ = upload(filename, "columns")
            assert (
                failed["index_status"] == "failed"
                and failed["error_code"] == "PDF_LAYOUT_UNSUPPORTED"
            )
        checks.append(
            "ambiguous three-column and rotated documents fail durably without fallback"
        )
        checks.append(
            "operator receives physical page-2 citation; withdrawal revokes it"
        )
        if args.require_s3:
            checks.append(
                "both layout originals archive in S3 and download byte-for-byte"
            )
    report = {
        "phase": "M7.5",
        "profile": "mock",
        "completed_at": datetime.now(UTC).isoformat(),
        "checks": checks,
        "fixed_comparison": comparison,
        "limitations": "Two synthetic documents only; no general PDF accuracy or semantic table reconstruction claim.",
    }
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
