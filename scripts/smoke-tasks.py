"""Run a synthetic order query through Go -> durable Agent graph -> Go -> SSE."""
import json
import os
import time
from contextlib import contextmanager
from pathlib import Path
from uuid import uuid4

import httpx

root = Path(__file__).resolve().parents[1]
settings = dict(line.split("=", 1) for line in (root / ".env").read_text(encoding="utf-8-sig").splitlines() if line and not line.startswith("#"))
base = os.getenv("FLOWDESK_SMOKE_URL", "http://localhost:" + settings["WEB_HOST_PORT"])


@contextmanager
def login(account):
    client = httpx.Client(base_url=base, timeout=15, trust_env=False,
                         headers={"Origin": base, "X-FlowDesk-Client": "web"})
    response = client.post("/api/v1/auth/login", json={"account": account, "password": settings["DEMO_PASSWORD"]})
    assert response.status_code == 200, "login failed"
    client.headers["Authorization"] = "Bearer " + response.json()["data"]["access_token"]
    try:
        yield client
    finally:
        client.close()


with login("demo.operator") as browser, login("other.operator") as other:
    request_key = str(uuid4())
    response = browser.post("/api/v1/conversations", json={"title": "M2 synthetic smoke"}, headers={"Idempotency-Key": request_key})
    assert response.status_code == 201, f"create: {response.status_code}"
    conversation = response.json()["data"]["id"]
    repeated = browser.post("/api/v1/conversations", json={"title": "M2 synthetic smoke"}, headers={"Idempotency-Key": request_key})
    assert repeated.json()["data"]["id"] == conversation
    body = {"client_message_id": str(uuid4()), "content": "查询 FD202611020002 的订单状态和物流", "attachments": []}
    send_key = str(uuid4())
    response = browser.post(f"/api/v1/conversations/{conversation}/messages", json=body, headers={"Idempotency-Key": send_key})
    assert response.status_code == 202, f"send: {response.status_code}"
    run_id = response.json()["data"]["id"]
    repeated = browser.post(f"/api/v1/conversations/{conversation}/messages", json=body, headers={"Idempotency-Key": send_key})
    assert repeated.json()["data"]["id"] == run_id
    assert other.get(f"/api/v1/runs/{run_id}").status_code == 404
    assert other.get(f"/api/v1/conversations/{conversation}/messages").status_code == 404
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        response = browser.get(f"/api/v1/runs/{run_id}")
        assert response.status_code == 200
        run = response.json()["data"]
        if run["status"] in ("succeeded", "failed", "expired"):
            break
        time.sleep(0.25)
    assert run["status"] == "succeeded", f"run: {run['status']} {run['error_code']}"
    assert "102.90" in run["current_message"]["content"]
    assert "来源更新时间" in run["current_message"]["content"]
    assert run["tool_call_count"] == 2
    events = browser.get(f"/api/v1/runs/{run_id}/events")
    assert events.status_code == 200 and "text/event-stream" in events.headers["content-type"]
    frames = [json.loads(line[6:]) for line in events.text.splitlines() if line.startswith("data: ")]
    assert [frame["seq"] for frame in frames] == list(range(1, run["last_event_seq"] + 1))
    resume = browser.get(f"/api/v1/runs/{run_id}/events", headers={"Last-Event-ID": "2"})
    assert [json.loads(line[6:])["seq"] for line in resume.text.splitlines() if line.startswith("data: ")] == list(range(3, run["last_event_seq"] + 1))
    assert browser.get(f"/api/v1/runs/{run_id}/events", headers={"Last-Event-ID": "999999"}).status_code == 400
    internal = httpx.post(
        "http://localhost:" + settings["API_HOST_PORT"] + f"/internal/v1/runs/{run_id}/lease",
        json={}, headers={"Authorization": browser.headers["Authorization"]}, trust_env=False,
    )
    assert internal.status_code == 401, "browser JWT accepted as service credential"
    messages = browser.get(f"/api/v1/conversations/{conversation}/messages").json()["data"]["items"]
    assert len(messages) == 2 and messages[1]["content"] == run["current_message"]["content"]
    browser.post("/api/v1/auth/logout")
    other.post("/api/v1/auth/logout")
print("M2 HTTP smoke passed: query, decimals, tool count, idempotency, tenant fencing, SSE replay, snapshot")
