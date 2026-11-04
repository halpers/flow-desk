"""Synthetic acceptance: real mock graph failures, scoped traces and safe retry replay."""

import argparse
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
        if line and not line.startswith("#") and "=" in line
    )
    assert settings.get("MODEL_PROVIDER", "mock") == "mock", (
        "Requires a disposable mock stack"
    )
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    report = {"phase": "M7.2", "profile": "mock", "completed_at": "", "checks": []}
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
            return response.json()["data"]

        token = call(
            "POST",
            "/auth/login",
            {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]},
        )["access_token"]
        http.headers["Authorization"] = "Bearer " + token
        conversation = call(
            "POST", "/conversations", {"title": "Synthetic operations check"}, 201
        )
        run = call(
            "POST",
            f"/conversations/{conversation['id']}/messages",
            {
                "client_message_id": str(uuid4()),
                "content": "查询 FD209901010001",
                "attachments": [],
            },
            202,
        )

        def wait_failed(run_id):
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                snapshot = call("GET", "/runs/" + run_id)
                if snapshot["status"] == "failed":
                    return snapshot
                assert snapshot["status"] in ("queued", "running")
                time.sleep(0.2)
            raise AssertionError("Synthetic missing-order task did not fail")

        failed = wait_failed(run["id"])
        trace = call("GET", f"/runs/{run['id']}/trace")
        assert trace["run"]["can_retry"] and trace["observation"]["available"]
        assert any(node["status"] == "failed" for node in trace["observation"]["nodes"])
        assert trace["observation"]["usage"]["model_attempts"] == 0
        assert "FD209901010001" not in json.dumps(trace)
        report["checks"].append("authorized sanitized real failure trace")
        key = str(uuid4())
        body = {"expected_version": failed["version"]}
        child = call("POST", f"/runs/{run['id']}/retry", body, 202, key)
        for retry_key in (key, str(uuid4())):
            replay = call("POST", f"/runs/{run['id']}/retry", body, 202, retry_key)
            assert replay["id"] == child["id"]
        assert child["retry_of_run_id"] == run["id"] and child["retry_depth"] == 1
        wait_failed(child["id"])
        messages = call("GET", f"/conversations/{conversation['id']}/messages")
        assert len(messages["items"]) == 4
        original = call("GET", "/runs?scope=own&status=failed&run_id=" + run["id"])
        assert len(original["items"]) == 1
        assert original["items"][0]["retried_by_run_id"] == child["id"]
        assert not original["items"][0]["can_retry"]
        assert call("GET", "/runs/" + run["id"])["version"] == failed["version"]
        report["checks"].append(
            "same-key and different-key replay keep one child and old terminal"
        )
    report["completed_at"] = datetime.now(UTC).isoformat()
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
