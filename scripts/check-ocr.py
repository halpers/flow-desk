"""Public API acceptance against a disposable mock stack using a real scan."""

import argparse
import base64
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
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings.get("MODEL_PROVIDER", "mock") == "mock", (
        "Use a disposable mock stack"
    )
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    checks = []
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=10,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

        def call(method, path, body=None, expected=200, key=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            assert response.status_code == expected, (path, response.status_code)
            return response.json().get("data")

        token = call(
            "POST",
            "/auth/login",
            {"account": "demo.admin", "password": settings["DEMO_PASSWORD"]},
        )["access_token"]
        http.headers["Authorization"] = "Bearer " + token
        base = call(
            "POST",
            "/knowledge-bases",
            {
                "name": "Synthetic OCR " + str(uuid4()),
                "read_roles": ["admin", "operator"],
            },
            201,
        )
        body = {
            "title": "OCR 极光保修",
            "filename": "ocr-policy.pdf",
            "ocr_mode": "auto",
            "content_base64": base64.b64encode(
                (ROOT / "flow-desk-agent/tests/fixtures/ocr-policy.pdf").read_bytes()
            ).decode(),
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
        deadline = time.monotonic() + 75
        while time.monotonic() < deadline:
            rows = call("GET", f"/documents/{version['document_id']}/versions")["items"]
            ready = next(row for row in rows if row["id"] == version["id"])
            if ready["index_status"] != "pending":
                break
            time.sleep(0.3)
        assert ready["index_status"] == "ready" and ready["ocr_page_count"] == 2, ready[
            "index_status"
        ]
        pages = call(
            "GET", f"/documents/{version['document_id']}/versions/{version['id']}/pages"
        )["pages"]
        assert (
            pages[1]["page_number"] == 2
            and pages[1]["extraction_method"] == "ocr"
            and "三十六个月" in pages[1]["text"]
        )
        checks.append(
            "real Chinese/English scan, immutable upload replay and physical page preview"
        )
        activation = {"version_id": version["id"], "expected_version": 1}
        call("POST", f"/documents/{version['document_id']}/activate", activation, 409)
        activation["confirm_ocr"] = True
        active = call(
            "POST", f"/documents/{version['document_id']}/activate", activation
        )
        checks.append("server-enforced OCR review before activation")
        citation_id = None
        try:
            token = call(
                "POST",
                "/auth/login",
                {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]},
            )["access_token"]
            http.headers["Authorization"] = "Bearer " + token
            conversation = call(
                "POST", "/conversations", {"title": "Synthetic OCR retrieval"}, 201
            )
            run = call(
                "POST",
                f"/conversations/{conversation['id']}/messages",
                {
                    "client_message_id": str(uuid4()),
                    "content": "极光保修政策规定设备保修期限多久？",
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
            citations = snapshot["current_message"]["citations"]
            for item in citations:
                evidence = call("GET", "/citations/" + item["id"])
                if (
                    evidence["version_id"] == version["id"]
                    and evidence["page_number"] == 2
                ):
                    assert evidence["extraction_method"] == "ocr"
                    citation_id = item["id"]
            assert citation_id, "No OCR page-2 evidence returned"
            checks.append("operator retrieves OCR text with Go-owned source metadata")
        finally:
            token = call(
                "POST",
                "/auth/login",
                {"account": "demo.admin", "password": settings["DEMO_PASSWORD"]},
            )["access_token"]
            http.headers["Authorization"] = "Bearer " + token
            call(
                "POST",
                f"/documents/{active['id']}/withdraw",
                {"expected_version": active["version"]},
            )
        if citation_id:
            token = call(
                "POST",
                "/auth/login",
                {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]},
            )["access_token"]
            http.headers["Authorization"] = "Bearer " + token
            call("GET", "/citations/" + citation_id, expected=409)
            checks.append("withdrawal blocks OCR citation")
    report = {
        "phase": "M7.3",
        "profile": "mock",
        "completed_at": datetime.now(UTC).isoformat(),
        "checks": checks,
    }
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
