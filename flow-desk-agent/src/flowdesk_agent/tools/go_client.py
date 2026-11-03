"""Narrow Go client. Credentials and trusted context never become model arguments."""

import asyncio
import json
import time
from collections.abc import Awaitable, Callable
from pathlib import Path
from uuid import uuid5

import httpx
from jsonschema import Draft202012Validator

from flowdesk_agent.api.schemas import ExecutionCommand, TrustedContext
from flowdesk_agent.storage.callbacks import CallbackStore
from flowdesk_agent.storage.tool_cache import ToolCache

directory = Path(__file__).resolve().parent
argument_validator = Draft202012Validator(json.loads((directory / "read.schema.json").read_text()))
action_validator = Draft202012Validator(json.loads((directory / "actions.schema.json").read_text()))
go_contract = json.loads((directory / "go.openapi.json").read_text())
result_validator = Draft202012Validator(
    go_contract["paths"]["/internal/v1/tools/{tool_name}"]["post"]["responses"]["200"]["content"][
        "application/json"
    ]["schema"]
)


class ToolError(Exception):
    def __init__(self, code: str):
        self.code = code
        super().__init__(code)


class OwnershipLost(Exception):
    pass


class GoTools:
    def __init__(
        self,
        http: httpx.AsyncClient,
        command: ExecutionCommand,
        callbacks: CallbackStore,
        cache: ToolCache,
        guard: Callable[[], Awaitable[None]],
        mcp=None,
    ):
        self.http = http
        self.command = command
        self.callbacks = callbacks
        self.cache = cache
        self.guard = guard
        self.mcp = mcp
        self.context: TrustedContext | None = None

    async def authorize(self) -> TrustedContext:
        await self.guard()
        response = await self.http.post(
            f"/internal/v1/runs/{self.command.run_id}/lease",
            json={
                "dispatch_id": str(self.command.dispatch_id),
                "lease_generation": self.command.lease_generation,
            },
        )
        if response.status_code != 200:
            raise ToolError("CAPABILITY_REVOKED")
        self.context = TrustedContext.model_validate(response.json()["data"])
        if (
            self.context.run_id != self.command.run_id
            or self.context.lease_generation != self.command.lease_generation
        ):
            raise ToolError("LEASE_STALE")
        return self.context

    async def call(self, tool: str, arguments: dict):
        argument_validator.validate({"tool_name": tool, "arguments": arguments})
        context = await self.authorize()
        if tool not in context.tools:
            raise ToolError("PERMISSION_DENIED")
        call_id = uuid5(
            self.command.dispatch_id, tool + ":" + json.dumps(arguments, sort_keys=True)
        )
        started = await self.callbacks.emit(
            self.command,
            "tool.started",
            {
                "tool_call_id": str(call_id),
                "tool_name": tool,
                "summary": {
                    "order.lookup": "查询订单",
                    "logistics.track": "查询物流",
                    "ticket.search": "查询工单",
                }[tool],
            },
            str(call_id),
        )
        await self.callbacks.wait(started)
        result = await self.cache.find(call_id)
        begin = time.monotonic()
        if result is None:
            if self.mcp is not None:
                await self.guard()
                result = await self.mcp.call(tool, arguments)
                result_validator.validate({"code": "OK", "request_id": "mcp", "data": result})
                await self.guard()
                await self.cache.insert(call_id, self.command.dispatch_id, tool, arguments, result)
            else:
                result = await self.read_http(tool, arguments, context.capability)
                await self.cache.insert(call_id, self.command.dispatch_id, tool, arguments, result)
        assert result is not None
        completed = await self.callbacks.emit(
            self.command,
            "tool.completed",
            {
                "tool_call_id": str(call_id),
                "tool_name": tool,
                "summary": "查询完成",
                "duration_ms": min(120000, round((time.monotonic() - begin) * 1000)),
            },
            str(call_id),
        )
        await self.callbacks.wait(completed)
        return result

    async def read_http(self, tool: str, arguments: dict, capability: str):
        for attempt in range(3):
            await self.guard()
            try:
                response = await self.http.post(
                    f"/internal/v1/tools/{tool}",
                    json={"arguments": arguments},
                    headers={"X-Run-Capability": capability},
                )
                if response.status_code >= 500:
                    raise httpx.TransportError("upstream unavailable")
                if response.status_code != 200:
                    raise ToolError(response.json().get("code", "TOOL_FAILED"))
                body = response.json()
                result_validator.validate(body)
                result = body["data"]
                return result
            except httpx.TransportError:
                if attempt == 2:
                    raise ToolError("UPSTREAM_UNAVAILABLE") from None
                await asyncio.sleep(0.25 * (2**attempt))

    async def action_request(self, path: str, body: dict | None = None, method: str = "POST"):
        context = await self.authorize()
        for attempt in range(3):
            await self.guard()
            try:
                response = await self.http.request(
                    method,
                    f"/internal/v1/runs/{self.command.run_id}{path}",
                    json=body,
                    headers={"X-Run-Capability": context.capability},
                )
                if response.status_code >= 500:
                    raise httpx.TransportError("action upstream unavailable")
                if response.status_code != 200:
                    raise ToolError(response.json().get("code", "ACTION_FAILED"))
                return response.json()["data"]
            except (httpx.TransportError, httpx.TimeoutException):
                if attempt == 2:
                    raise ToolError("WRITE_RESULT_UNKNOWN") from None
                await asyncio.sleep(0.25 * (2**attempt))

    async def prepare_action(
        self, action_id: str, tool: str, arguments: dict, evidence_ids: list[str]
    ):
        action_validator.validate({"tool_name": tool, "arguments": arguments})
        return await self.action_request(
            "/actions",
            {
                "action_id": action_id,
                "tool_name": tool,
                "arguments": arguments,
                "evidence_ids": evidence_ids,
            },
        )

    async def execute_action(self, action_id: str):
        # A response lost after COMMIT is safely replayed with this exact ID.
        if self.mcp is None:
            result = await self.action_request(f"/actions/{action_id}/execute", {})
        else:
            await self.authorize()
            result = await self.mcp.call("actions.execute", {"action_id": action_id})
            await self.guard()
        contract = go_contract["paths"]["/internal/v1/runs/{run_id}/actions/{action_id}/execute"][
            "post"
        ]["responses"]["200"]["content"]["application/json"]["schema"]
        Draft202012Validator(contract).validate(
            {"code": "OK", "request_id": "validated", "data": result}
        )
        if result["action_id"] != action_id or result["business_object_type"] != "ticket":
            raise ToolError("RECEIPT_MISMATCH")
        return result
