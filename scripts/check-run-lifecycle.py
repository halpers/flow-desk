"""Read-only lifecycle acceptance against an explicitly disposable mock stack."""

import argparse
import json
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
from acceptance_http import login_with_backoff

ROOT = Path(__file__).resolve().parents[1]
TERMINAL = {"succeeded", "failed", "cancelled", "expired"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    env_path = args.env_file.resolve()
    assert env_path.is_relative_to(ROOT / "work"), (
        "Requires work-local disposable configuration"
    )
    settings = dict(
        line.split("=", 1)
        for line in env_path.read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#") and "=" in line
    )
    assert settings.get("MODEL_PROVIDER") == "mock", "Requires synthetic mock stack"
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    report = {"phase": "M7.21", "profile": "mock", "checks": []}
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=15,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:
        response = login_with_backoff(http, "demo.operator", settings["DEMO_PASSWORD"])
        assert response.status_code == 200, ("login", response.status_code)
        http.headers["Authorization"] = (
            "Bearer " + response.json()["data"]["access_token"]
        )

        def call(method, path, body=None, expected=200):
            response = http.request(
                method, path, json=body, headers={"Idempotency-Key": str(uuid4())}
            )
            assert response.status_code == expected, (
                method,
                path,
                response.status_code,
                response.json().get("code"),
            )
            return response

        conversation = call(
            "POST", "/conversations", {"title": "Synthetic lifecycle"}, 201
        ).json()["data"]
        run = call(
            "POST",
            f"/conversations/{conversation['id']}/messages",
            {
                "client_message_id": str(uuid4()),
                "content": "查询 FD209901010001",
                "attachments": [],
            },
            202,
        ).json()["data"]
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            snapshot = call("GET", "/runs/" + run["id"]).json()["data"]
            if snapshot["status"] in TERMINAL:
                break
            time.sleep(0.2)
        else:
            raise AssertionError("Synthetic task did not reach terminal state")
        assert snapshot["status"] == "failed"
        path = f"/runs/{run['id']}/lifecycle"
        response = call("GET", path)
        assert response.headers.get("cache-control") == "no-store"
        data = response.json()["data"]
        assert data["observation_only"] and not data["archive_candidate"]
        assert data["minimum_age_days"] == 30
        assert "MINIMUM_AGE_NOT_REACHED" in data["protection_reasons"]
        assert data["business"]["message_count"] == 2
        assert data["business"]["dispatch_count"] >= 1
        assert data["agent"]["available"] and data["agent"]["command_count"] >= 1
        assert "FD209901010001" not in json.dumps(data)
        assert not any(
            field in json.dumps(data)
            for field in ('"content"', '"arguments"', '"checkpoint"')
        )
        report["checks"].append("authorized metadata-only counts and 30-day protection")
        before = call("GET", f"/conversations/{conversation['id']}/messages").json()[
            "data"
        ]
        before_run = call("GET", "/runs/" + run["id"]).json()["data"]
        for _ in range(3):
            call("GET", path)
        after = call("GET", f"/conversations/{conversation['id']}/messages").json()[
            "data"
        ]
        after_run = call("GET", "/runs/" + run["id"]).json()["data"]
        assert before == after and before_run == after_run
        report["checks"].append(
            "repeated preview preserves public messages and task state"
        )
        child = call(
            "POST",
            f"/runs/{run['id']}/retry",
            {
                "expected_version": snapshot["version"],
            },
            202,
        ).json()["data"]
        assert child["retry_of_run_id"] == run["id"]
        data = call("GET", path).json()["data"]
        assert "RETRY_CHAIN_REFERENCES" in data["protection_reasons"]
        assert (
            data["business"]["retry_link_count"] == 1 and not data["archive_candidate"]
        )
        report["checks"].append("retry references protect original task")
        http.headers.pop("Authorization")
        call("GET", path, expected=401)
        report["checks"].append("unauthenticated preview rejected")
    report["completed_at"] = datetime.now(UTC).isoformat()
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps({"phase": "M7.21", "passed": len(report["checks"])}))


if __name__ == "__main__":
    main()
