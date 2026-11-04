"""Fixed synthetic M6.5 acceptance against real public APIs and business transactions."""

import argparse
import hashlib
import json
import os
import ssl
import subprocess
import time
from contextlib import ExitStack
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx

ROOT = Path(__file__).resolve().parents[1]
ORDER_NO = "FD202611020002"
TERMINAL = {"succeeded", "failed", "expired", "cancelled"}


def compose(*arguments):
    if os.name == "nt":
        path = "/mnt/" + ROOT.drive[0].lower() + ROOT.as_posix()[2:]
        prefix = ["wsl.exe", "--cd", path, "--exec", "docker", "compose"]
    else:
        prefix = ["docker", "compose"]
    subprocess.run(prefix + list(arguments), cwd=ROOT, check=True, timeout=90)


class API:
    def __init__(self, client):
        self.client = client
        self.runs = set()

    def call(self, method, path, body=None, expected=200, key=None):
        response = self.client.request(
            method,
            "/api/v1" + path,
            json=body,
            headers={"Idempotency-Key": key or str(uuid4())},
        )
        assert response.status_code == expected, (
            f"HTTP {response.status_code}: {response.json().get('code')}"
        )
        return response.json()["data"]

    def start(self, content):
        conversation = self.call(
            "POST", "/conversations", {"title": "M6.5 synthetic scenario"}, 201
        )
        run = self.call(
            "POST",
            f"/conversations/{conversation['id']}/messages",
            {"client_message_id": str(uuid4()), "content": content, "attachments": []},
            202,
        )
        self.runs.add(run["id"])
        return run

    def wait(self, run, status):
        deadline = time.monotonic() + 125
        while time.monotonic() < deadline:
            current = self.call("GET", f"/runs/{run['id']}")
            if current["status"] == status:
                return current
            assert current["status"] not in TERMINAL, (
                f"Run {current['status']}: {current['error_code']}"
            )
            time.sleep(0.2)
        raise AssertionError(f"Run did not reach {status}")

    def cancel(self, run):
        return self.call("POST", f"/runs/{run['id']}/cancel", {"expected_version": run["version"]})

    def decide(self, run, decision="approve", key=None):
        approval = next(item for item in run["approvals"] if item["status"] == "pending")
        body = {
            "expected_version": approval["version"],
            "decision": decision,
            "reason": "Synthetic acceptance",
        }
        return self.call("POST", f"/approvals/{approval['id']}/decision", body, key=key)

    def close_runs(self):
        for identifier in self.runs:
            run = self.call("GET", f"/runs/{identifier}")
            if run["status"] not in TERMINAL:
                self.cancel(run)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tls", action="store_true")
    parser.add_argument("--restart-agent", action="store_true")
    parser.add_argument("--transport", choices=["http", "mcp"], default="http")
    parser.add_argument("--output", type=Path, default=ROOT / "reports/m6/business-scenarios.json")
    args = parser.parse_args()
    settings = dict(
        line.split("=", 1)
        for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#")
    )
    # Inspect only assertions, never print Docker configuration or provider keys.
    compose(
        "exec",
        "-T",
        "agent",
        "python",
        "-c",
        "import os; assert os.environ['MODEL_PROVIDER']=='mock'; "
        "assert not os.environ.get('MODEL_API_KEY'); "
        "assert not os.environ.get('EMBEDDING_API_KEY'); "
        f"assert os.environ.get('TOOL_TRANSPORT','http')=={args.transport!r}; "
        "print('Verified deterministic model and empty provider keys')",
    )
    dataset_path = ROOT / "flow-desk-agent/eval/datasets/m6-business.jsonl"
    dataset = [
        json.loads(line) for line in dataset_path.read_text(encoding="utf-8").splitlines() if line
    ]
    assert len(dataset) == 12 and len({item["id"] for item in dataset}) == 12
    base = (
        "https://localhost:" + settings.get("TLS_HOST_PORT", "13443")
        if args.tls
        else "http://localhost:" + settings["WEB_HOST_PORT"]
    )
    options = {
        "base_url": base,
        "timeout": 20,
        "trust_env": False,
        "headers": {"Origin": base, "X-FlowDesk-Client": "web"},
        "verify": ssl.create_default_context(cafile=str(ROOT / "runtime/tls/localhost.crt"))
        if args.tls
        else True,
    }
    report = {
        "phase": "M6.5",
        "profile": "mock",
        "tool_transport": args.transport,
        "tls_verified": args.tls,
        "started_at": datetime.now(UTC).isoformat(),
        "dataset_sha256": hashlib.sha256(dataset_path.read_bytes()).hexdigest(),
        "restart_verified": False,
        "cases": [],
    }
    failed = False
    with ExitStack() as stack:
        actors = {}
        for role in ("supervisor", "admin", "operator"):
            api = API(stack.enter_context(httpx.Client(**options)))
            token = api.call(
                "POST",
                "/auth/login",
                {"account": "demo." + role, "password": settings["DEMO_PASSWORD"]},
            )
            api.client.headers["Authorization"] = "Bearer " + token["access_token"]
            actors[role] = api
        admin, handler = actors["admin"], actors["supervisor"]
        actor_id = handler.call("GET", "/auth/me")["id"]
        admin_id = admin.call("GET", "/auth/me")["id"]
        order = admin.call("GET", f"/orders?order_no={ORDER_NO}&limit=1")["items"][0]
        primary = None

        def create():
            return admin.call(
                "POST",
                "/tickets",
                {
                    "order_id": order["id"],
                    "expected_version": order["version"],
                    "title": "Synthetic M6.5 fixture",
                    "description": "Synthetic only",
                    "category": "logistics",
                    "priority": "normal",
                },
                201,
            )

        def current(ticket):
            return admin.call("GET", f"/tickets/{ticket['id']}")

        def events(ticket):
            return admin.call("GET", f"/tickets/{ticket['id']}/events?limit=100")["items"]

        def instruction(ticket, resolve=False):
            verb = "办结" if resolve else "接手处理"
            field = "处理结论" if resolve else "处理原因"
            return (
                f"请{verb}工单 {ticket['ticket_no']}，订单 {ORDER_NO}，{field}：合成配送回访已完成"
            )

        try:
            for case in dataset:
                started = time.monotonic()
                result = {
                    "id": case["id"],
                    "scenario": case["scenario"],
                    "passed": False,
                }
                try:
                    scenario = case["scenario"]
                    if scenario == "logistics_create":
                        run = handler.start(f"请创建物流工单，跟进订单 {ORDER_NO} 配送问题")
                        waiting = handler.wait(run, "waiting_approval")
                        assert (
                            waiting["approvals"][0]["display_snapshot"]["arguments"]["category"]
                            == "logistics"
                        )
                        handler.decide(waiting)
                        done = handler.wait(run, "succeeded")
                        receipt = done["action_receipts"][0]
                        primary = current({"id": receipt["business_object_id"]})
                        assert (
                            primary["category"] == "logistics"
                            and primary["status"] == case["expected_status"]
                        )
                    elif scenario in ("start_processing", "resolve"):
                        assert primary is not None
                        before = current(primary)
                        run = handler.start(instruction(before, scenario == "resolve"))
                        waiting = handler.wait(run, "waiting_approval")
                        assert current(primary) == before, "Unapproved effect"
                        assert (
                            waiting["approvals"][0]["display_snapshot"]["from_status"]
                            == before["status"]
                        )
                        if scenario == "start_processing" and args.restart_agent:
                            recovery_started = time.monotonic()
                            compose("restart", "agent")
                            assert (
                                handler.call("GET", f"/runs/{run['id']}")["approvals"]
                                == waiting["approvals"]
                            )
                            report["restart_verified"] = True
                        key = str(uuid4())
                        handler.decide(
                            waiting, key=key
                        )  # Deliberately discard this accepted response.
                        done = handler.wait(run, "succeeded")
                        replay = handler.decide(waiting, key=key)
                        assert replay["id"] == done["id"]
                        assert len(done["action_receipts"]) == 1
                        receipt = done["action_receipts"][0]
                        primary = current(primary)
                        assert (
                            primary["status"] == case["expected_status"]
                            and primary["version"] == before["version"] + 1
                        )
                        assert primary["assignee_user_id"] == actor_id
                        assert receipt["result"]["status"] == primary["status"]
                        operation = (
                            "ticket.resolved"
                            if scenario == "resolve"
                            else "ticket.processing_started"
                        )
                        assert sum(item["operation"] == operation for item in events(primary)) == 1
                        if scenario == "resolve":
                            assert receipt["result"]["note_id"]
                        if scenario == "start_processing" and args.restart_agent:
                            report["restart_to_completed_ms"] = round(
                                (time.monotonic() - recovery_started) * 1000
                            )
                            assert report["restart_to_completed_ms"] < 60000
                    else:
                        ticket = create()
                        api = handler
                        if scenario in ("cancel", "resolve_other_assignee"):
                            ticket = admin.call(
                                "PATCH",
                                f"/tickets/{ticket['id']}/status",
                                {
                                    "expected_version": ticket["version"],
                                    "status": "in_progress",
                                },
                            )
                        if scenario in ("assigned_elsewhere", "resolve_other_assignee"):
                            ticket = admin.call(
                                "PATCH",
                                f"/tickets/{ticket['id']}/assignment",
                                {
                                    "expected_version": ticket["version"],
                                    "assignee_user_id": admin_id,
                                },
                            )
                        if scenario == "cancel":
                            ticket = admin.call(
                                "PATCH",
                                f"/tickets/{ticket['id']}/assignment",
                                {
                                    "expected_version": ticket["version"],
                                    "assignee_user_id": actor_id,
                                },
                            )
                        if scenario == "operator_denied":
                            api = actors["operator"]
                        if scenario == "team_and_tenant_denied":
                            team = admin.call(
                                "POST",
                                "/teams",
                                {"name": "Synthetic " + uuid4().hex[:12]},
                                201,
                            )
                            ticket = admin.call(
                                "PATCH",
                                f"/tickets/{ticket['id']}/team",
                                {
                                    "expected_version": ticket["version"],
                                    "team_id": team["id"],
                                },
                            )
                            api.call("GET", f"/tickets/{ticket['id']}", expected=404)
                            foreign = API(stack.enter_context(httpx.Client(**options)))
                            token = foreign.call(
                                "POST",
                                "/auth/login",
                                {
                                    "account": "other.supervisor",
                                    "password": settings["DEMO_PASSWORD"],
                                },
                            )
                            foreign.client.headers["Authorization"] = (
                                "Bearer " + token["access_token"]
                            )
                            foreign.call("GET", f"/tickets/{ticket['id']}", expected=404)
                            foreign.call(
                                "POST",
                                f"/tickets/{ticket['id']}/notes",
                                {
                                    "expected_version": ticket["version"],
                                    "content": "Denied",
                                },
                                expected=404,
                            )
                        before_events = events(ticket)
                        run = api.start(
                            f"请接手处理工单，订单 {ORDER_NO}"
                            if scenario == "ambiguous_selection"
                            else instruction(
                                ticket,
                                scenario
                                in (
                                    "cancel",
                                    "invalid_state",
                                    "resolve_other_assignee",
                                ),
                            )
                        )
                        if scenario in (
                            "ambiguous_selection",
                            "team_and_tenant_denied",
                        ):
                            pending = api.wait(run, "waiting_input")
                            assert not pending["approvals"] and not pending["action_receipts"]
                            if scenario == "ambiguous_selection":
                                api.call(
                                    "POST",
                                    f"/runs/{run['id']}/inputs",
                                    {
                                        "input_id": pending["pending_inputs"][0]["id"],
                                        "expected_version": pending["version"],
                                        "content": ticket["ticket_no"],
                                    },
                                    202,
                                )
                                pending = api.wait(run, "waiting_approval")
                                assert (
                                    pending["approvals"][0]["display_snapshot"]["arguments"][
                                        "ticket_id"
                                    ]
                                    == ticket["id"]
                                )
                            else:
                                assert (
                                    ticket["ticket_no"]
                                    not in pending["pending_inputs"][0]["question"]
                                )
                            api.cancel(pending)
                        elif scenario in ("reject", "cancel", "stale_proposal"):
                            pending = api.wait(run, "waiting_approval")
                            if scenario == "reject":
                                assert api.decide(pending, "reject")["status"] == "cancelled"
                            elif scenario == "cancel":
                                assert api.cancel(pending)["status"] == "cancelled"
                                approval = pending["approvals"][0]
                                api.call(
                                    "POST",
                                    f"/approvals/{approval['id']}/decision",
                                    {
                                        "expected_version": approval["version"],
                                        "decision": "approve",
                                    },
                                    expected=409,
                                )
                            else:
                                ticket = admin.call(
                                    "PATCH",
                                    f"/tickets/{ticket['id']}/status",
                                    {
                                        "expected_version": ticket["version"],
                                        "status": "in_progress",
                                    },
                                )
                                before_events = events(ticket)
                                api.decide(pending)
                                done = api.wait(run, "failed")
                                assert (
                                    done["error_code"] == case["expected_error"]
                                    and not done["action_receipts"]
                                ), f"Unexpected failure: {done['error_code']}"
                        else:
                            done = api.wait(run, "failed")
                            assert (
                                done["error_code"] == case["expected_error"]
                                and not done["action_receipts"]
                            ), f"Unexpected failure: {done['error_code']}"
                        assert current(ticket)["status"] == case["expected_status"]
                        assert events(ticket) == before_events, "Unexpected business effect"
                    if "expected_tool" in case:
                        assert receipt["tool_name"] == case["expected_tool"]
                        result["action_id"] = receipt["action_id"]
                    result.update({"passed": True, "run_id": run["id"]})
                except Exception as error:
                    failed = True
                    result["error_type"] = type(error).__name__
                    result["error"] = str(error)[:300]
                finally:
                    result["duration_ms"] = round((time.monotonic() - started) * 1000)
                    report["cases"].append(result)
                    for api in actors.values():
                        api.close_runs()
                print(case["id"], "passed" if result["passed"] else "FAILED", flush=True)
        finally:
            for api in actors.values():
                api.client.post("/api/v1/auth/logout")
            report["completed_at"] = datetime.now(UTC).isoformat()
            report["passed"] = sum(item["passed"] for item in report["cases"])
            report["total"] = len(dataset)
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(f"M6.5 synthetic acceptance: {report['passed']}/{report['total']}")
    if failed:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
