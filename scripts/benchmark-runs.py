"""Accepted-to-started progress with ten simultaneously active mock runs."""

import concurrent.futures
import json
import threading
import time
from datetime import UTC, datetime
from uuid import uuid4

from eval_common import ROOT, PublicAPI, settings
from evaluate import percentile

config = settings()
assert config.get("MODEL_PROVIDER", "mock") == "mock", "Use deterministic mock profile"
accounts = [
    "demo.operator",
    "demo.supervisor",
    "demo.admin",
    "other.operator",
    "other.admin",
] * 2
clients = [PublicAPI(config, account) for account in accounts]
barrier = threading.Barrier(10, timeout=30)


def measure(client):
    conversation = client.call(
        "POST", "/conversations", {"title": "M5 progress latency"}, 201
    )
    barrier.wait()
    begin = time.monotonic()
    run = client.call(
        "POST",
        f"/conversations/{conversation['id']}/messages",
        {
            "client_message_id": str(uuid4()),
            "content": "查询订单 FD202611020002 状态",
            "attachments": [],
        },
        202,
    )
    accepted = time.monotonic()
    barrier.wait()
    duration = None
    try:
        with client.http.stream("GET", f"/api/v1/runs/{run['id']}/events") as response:
            assert response.status_code == 200
            for line in response.iter_lines():
                if line.startswith("data: {"):
                    event = json.loads(line[6:])
                    if event["type"] == "run.started":
                        duration = round((time.monotonic() - accepted) * 1000, 3)
                        break
        assert duration is not None, "RUN_NEVER_STARTED"
        return {
            "run_id": run["id"],
            "accept_ms": round((accepted - begin) * 1000, 3),
            "first_progress_ms": duration,
        }
    finally:
        for _ in range(3):
            snapshot = client.call("GET", f"/runs/{run['id']}")
            if snapshot["status"] in ("succeeded", "failed", "cancelled", "expired"):
                break
            try:
                client.call(
                    "POST",
                    f"/runs/{run['id']}/cancel",
                    {"expected_version": snapshot["version"]},
                )
                break
            except AssertionError:
                continue


report = {
    "started_at": datetime.now(UTC).isoformat(),
    "profile": "mock",
    "active_runs": 10,
    "tenants": 2,
    "actors": 5,
    "runs_per_actor": 2,
    "definition": "HTTP 202 received to run.started SSE received; excludes queued event; includes all-accepted barrier",
    "target_p95_ms": 2000,
}
try:
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
        report["samples"] = list(executor.map(measure, clients))
    report["p95_ms"] = percentile(
        [item["first_progress_ms"] for item in report["samples"]], 0.95
    )
    report["target_met"] = report["p95_ms"] < 2000
finally:
    for client in clients:
        client.close()
    report["completed_at"] = datetime.now(UTC).isoformat()
    (ROOT / "reports/m5/run-performance.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
print(
    json.dumps(
        {key: value for key, value in report.items() if key != "samples"},
        ensure_ascii=True,
    )
)
