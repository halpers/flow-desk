"""Synthetic M4 acceptance through the public API, optionally restarting the Agent."""

import argparse
import json
import shutil
import subprocess
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--restart-agent", action="store_true")
    args = parser.parse_args()
    settings = dict(
        line.split("=", 1)
        for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#")
    )
    base = "http://localhost:" + settings["WEB_HOST_PORT"]
    report = {
        "phase": "M4",
        "profile": settings.get("MODEL_PROVIDER", "mock"),
        "started_at": datetime.now(UTC).isoformat(),
        "cases": [],
        "restart_verified": False,
    }

    def call(client, method, path, body=None, expected=200, key=None):
        response = client.request(
            method,
            "/api/v1" + path,
            json=body,
            headers={"Idempotency-Key": key or str(uuid4())},
        )
        assert response.status_code == expected, (
            f"{method} {path}: {response.status_code} {response.json().get('code')}"
        )
        return response.json()["data"]

    def login(client, account):
        token = call(
            client,
            "POST",
            "/auth/login",
            {"account": account, "password": settings["DEMO_PASSWORD"]},
        )
        client.headers["Authorization"] = "Bearer " + token["access_token"]

    def start(client, content):
        conversation = call(
            client, "POST", "/conversations", {"title": "M4 synthetic acceptance"}, 201
        )
        return call(
            client,
            "POST",
            f"/conversations/{conversation['id']}/messages",
            {"client_message_id": str(uuid4()), "content": content, "attachments": []},
            202,
        )

    def wait(client, run, status):
        deadline = time.monotonic() + 125
        while time.monotonic() < deadline:
            snapshot = call(client, "GET", f"/runs/{run['id']}")
            if snapshot["status"] == status:
                return snapshot
            assert snapshot["status"] not in (
                "succeeded",
                "failed",
                "expired",
                "cancelled",
            ), f"run: {snapshot['status']} {snapshot['error_code']}"
            time.sleep(0.2)
        raise AssertionError(f"run did not reach {status}")

    def decide(client, run, decision):
        approval = next(
            item for item in run["approvals"] if item["status"] == "pending"
        )
        path = f"/approvals/{approval['id']}/decision"
        body = {
            "expected_version": approval["version"],
            "decision": decision,
            "reason": "synthetic acceptance",
        }
        key = str(uuid4())
        result = call(client, "POST", path, body, key=key)
        assert call(client, "POST", path, body, key=key) == result
        return result

    options = {
        "base_url": base,
        "timeout": 15,
        "trust_env": False,
        "headers": {"Origin": base, "X-FlowDesk-Client": "web"},
    }
    with httpx.Client(**options) as client, httpx.Client(**options) as foreign:
        login(client, "demo.operator")
        login(foreign, "other.operator")
        try:
            order = call(client, "GET", "/orders?order_no=FD202611020002&limit=1")[
                "items"
            ][0]

            def tickets():
                return call(
                    client, "GET", f"/tickets?order_id={order['id']}&limit=100"
                )["items"]

            before = len(tickets())
            run = start(client, "请创建售后工单，跟进配送问题")
            missing = wait(client, run, "waiting_input")
            pending = missing["pending_inputs"][0]
            key = str(uuid4())
            body = {
                "input_id": pending["id"],
                "expected_version": missing["version"],
                "content": "FD202611020002",
            }
            queued = call(client, "POST", f"/runs/{run['id']}/inputs", body, 202, key)
            assert (
                call(client, "POST", f"/runs/{run['id']}/inputs", body, 202, key)
                == queued
            )
            waiting = wait(client, run, "waiting_approval")
            assert len(tickets()) == before, "write happened before approval"
            if args.restart_agent:
                restart_started = time.monotonic()
                shell = shutil.which("pwsh") or shutil.which("powershell")
                assert shell, "PowerShell is required for the Compose restart check"
                subprocess.run(
                    [
                        shell,
                        "-NoProfile",
                        "-File",
                        str(ROOT / "scripts/compose.ps1"),
                        "restart",
                        "agent",
                    ],
                    cwd=ROOT,
                    check=True,
                )
                assert (
                    call(client, "GET", f"/runs/{run['id']}")["approvals"]
                    == waiting["approvals"]
                )
                report["restart_verified"] = True
            decide(client, waiting, "approve")
            final = wait(client, run, "succeeded")
            if args.restart_agent:
                report["restart_to_completed_ms"] = round(
                    (time.monotonic() - restart_started) * 1000
                )
                assert report["restart_to_completed_ms"] < 60000, (
                    "restart recovery exceeded 60 seconds"
                )
            receipt = final["action_receipts"][0]
            assert len(tickets()) == before + 1
            assert receipt["result"]["ticket_no"] in final["current_message"]["content"]
            assert (
                foreign.get(
                    f"/api/v1/tickets/{receipt['business_object_id']}"
                ).status_code
                == 404
            )
            report["cases"].append(
                {
                    "name": "input, restart, approval and create",
                    "passed": True,
                    "run_id": run["id"],
                    "action_id": receipt["action_id"],
                }
            )
            note = start(
                client,
                f"为订单 FD202611020002 的工单 {receipt['result']['ticket_no']} 添加备注：合成配送回访已登记",
            )
            waiting = wait(client, note, "waiting_approval")
            decide(client, waiting, "approve")
            noted = wait(client, note, "succeeded")
            assert noted["action_receipts"][0]["tool_name"] == "ticket.add_note"
            timeline = call(
                client,
                "GET",
                f"/tickets/{receipt['business_object_id']}/events?limit=100",
            )
            assert (
                sum(
                    item["operation"] == "ticket.note_added"
                    for item in timeline["items"]
                )
                == 1
            )
            report["cases"].append(
                {"name": "approved note", "passed": True, "run_id": note["id"]}
            )
            rejected = start(client, "创建工单 FD202611020002")
            waiting = wait(client, rejected, "waiting_approval")
            assert decide(client, waiting, "reject")["status"] == "cancelled"
            report["cases"].append(
                {
                    "name": "rejection has no effect",
                    "passed": True,
                    "run_id": rejected["id"],
                }
            )
            cancelled = start(client, "创建工单 FD202611020002")
            waiting = wait(client, cancelled, "waiting_approval")
            result = call(
                client,
                "POST",
                f"/runs/{cancelled['id']}/cancel",
                {"expected_version": waiting["version"]},
            )
            assert result["status"] == "cancelled" and len(tickets()) == before + 1
            approval = waiting["approvals"][0]
            assert (
                client.post(
                    f"/api/v1/approvals/{approval['id']}/decision",
                    json={
                        "expected_version": approval["version"],
                        "decision": "approve",
                    },
                    headers={"Idempotency-Key": str(uuid4())},
                ).status_code
                == 409
            )
            replay = client.get(f"/api/v1/runs/{cancelled['id']}/events")
            assert replay.status_code == 200 and "run.cancelled" in replay.text
            report["cases"].append(
                {
                    "name": "cancel invalidates pending approval",
                    "passed": True,
                    "run_id": cancelled["id"],
                }
            )
        finally:
            client.post("/api/v1/auth/logout")
            foreign.post("/api/v1/auth/logout")
    report["completed_at"] = datetime.now(UTC).isoformat()
    directory = ROOT / "reports/m4"
    directory.mkdir(parents=True, exist_ok=True)
    (directory / ("acceptance-" + report["profile"] + ".json")).write_text(
        json.dumps(report, indent=2) + "\n", encoding="utf-8"
    )
    print(
        "M4 HTTP acceptance passed: 4/4; persistent waits, exact approvals, receipts, reject/cancel and tenant boundary"
    )


if __name__ == "__main__":
    main()
