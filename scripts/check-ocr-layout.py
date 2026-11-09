"""Joint OCR/layout public acceptance in an explicitly disposable mock stack."""

import argparse
import base64
import hashlib
import json
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
from acceptance_http import login_with_backoff

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "flow-desk-agent/tests/fixtures"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    environment = args.env_file.resolve()
    assert environment.is_relative_to(ROOT / "work"), "Requires disposable work configuration"
    settings = dict(
        line.split("=", 1)
        for line in environment.read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#") and "=" in line
    )
    assert settings.get("MODEL_PROVIDER") == "mock", "Requires a synthetic mock stack"
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    report = {"phase": "M7.24", "profile": "mock", "checks": [], "fixtures": []}
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=15,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

        def login(account):
            response = login_with_backoff(http, account, settings["DEMO_PASSWORD"])
            assert response.status_code == 200, ("login", response.status_code)
            http.headers["Authorization"] = "Bearer " + response.json()["data"]["access_token"]

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

        login("demo.admin")
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Synthetic OCR layout " + str(uuid4()),
                "read_roles": ["admin", "operator"],
            },
            201,
        )

        def upload(filename, mode, layout):
            raw = (FIXTURES / filename).read_bytes()
            body = {
                "title": "Synthetic joint " + filename,
                "filename": filename,
                "ocr_mode": mode,
                "pdf_layout": layout,
                "content_base64": base64.b64encode(raw).decode(),
            }
            path = f"/knowledge-bases/{base['id']}/documents"
            key = str(uuid4())
            version = call("POST", path, body, 202, key)
            assert call("POST", path, body, 202, key)["id"] == version["id"]
            call(
                "POST",
                path,
                {**body, "pdf_layout": "plain"},
                409,
                key,
                code="IDEMPOTENCY_CONFLICT",
            )
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline:
                current = next(
                    row
                    for row in call("GET", f"/documents/{version['document_id']}/versions")["items"]
                    if row["id"] == version["id"]
                )
                if current["index_status"] != "pending":
                    return current
                time.sleep(0.3)
            raise AssertionError("Joint OCR index did not finish")

        citation_id = None
        for filename, mode, layout in [
            ("ocr-layout-columns.pdf", "auto", "columns"),
            ("ocr-layout-table.pdf", "force", "table"),
            ("ocr-layout-mixed.pdf", "auto", "columns"),
        ]:
            version = upload(filename, mode, layout)
            assert version["index_status"] == "ready", version.get("error_code")
            preview = call(
                "GET",
                f"/documents/{version['document_id']}/versions/{version['id']}/pages",
            )
            assert preview["parser_version"] == f"pypdf-6.21.4-tesseract-5.5.0-{layout}-v1"
            pages = preview["pages"]
            assert [page["page_number"] for page in pages] == list(range(1, len(pages) + 1))
            if layout == "table":
                row = next(line for line in pages[0]["text"].splitlines() if "六十个月" in line)
                assert "六十个月" in row and "设备编号与发票" in row and "二十四个月" not in row
            else:
                assert pages[0]["text"].index("LEFT-5") < pages[0]["text"].index("RIGHT-1")
                assert "四十八个月" in pages[1]["text"]
                assert pages[1]["extraction_method"] == "ocr"
                assert pages[0]["extraction_method"] == ("text" if "mixed" in filename else "ocr")
            report["fixtures"].append(
                {
                    "filename": filename,
                    "sha256": hashlib.sha256((FIXTURES / filename).read_bytes()).hexdigest(),
                    "pages": len(pages),
                    "parser": preview["parser_version"],
                }
            )
            path = f"/documents/{version['document_id']}/activate"
            activation = {"version_id": version["id"], "expected_version": 1}
            call(
                "POST",
                path,
                {**activation, "confirm_layout": True},
                409,
                code="OCR_REVIEW_REQUIRED",
            )
            call(
                "POST",
                path,
                {**activation, "confirm_ocr": True},
                409,
                code="PDF_LAYOUT_REVIEW_REQUIRED",
            )
            activation.update(confirm_ocr=True, confirm_layout=True)
            login("demo.operator")
            call("POST", path, activation, 403)
            login("demo.admin")
            active = call("POST", path, activation)
            try:
                if filename == "ocr-layout-columns.pdf":
                    login("demo.operator")
                    conversation = call(
                        "POST",
                        "/conversations",
                        {"title": "Synthetic joint OCR citation"},
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
                    for item in snapshot["current_message"]["citations"]:
                        evidence = call("GET", "/citations/" + item["id"])
                        if evidence["version_id"] == version["id"] and evidence["page_number"] == 2:
                            assert (
                                evidence["extraction_method"] == "ocr"
                                and "四十八个月" in evidence["excerpt"]
                            )
                            citation_id = item["id"]
                    assert citation_id, "No joint OCR physical page-2 citation"
            finally:
                login("demo.admin")
                call(
                    "POST",
                    f"/documents/{active['id']}/withdraw",
                    {"expected_version": active["version"]},
                )
            if filename == "ocr-layout-columns.pdf":
                login("demo.operator")
                call(
                    "GET",
                    "/citations/" + citation_id,
                    expected=409,
                    code="EVIDENCE_REVOKED",
                )
                login("demo.admin")
        report["checks"].extend(
            [
                "native column/table and mixed text/OCR pages with immutable upload replay",
                "both server-enforced reviews and operator denial",
                "physical page-2 OCR citation and withdrawal revocation",
            ]
        )
        failed = upload("ocr-layout-ambiguous.pdf", "auto", "columns")
        assert (
            failed["index_status"] == "failed" and failed["error_code"] == "PDF_LAYOUT_UNSUPPORTED"
        )
        report["checks"].append("ambiguous three-column scan fails durably without plain fallback")
    report["completed_at"] = datetime.now(UTC).isoformat()
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps({"phase": report["phase"], "passed": len(report["checks"])}))


if __name__ == "__main__":
    main()
