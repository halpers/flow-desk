import asyncio
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from uuid import uuid4

import httpx
from mcp import Client
from test_durable_runtime import command

from flowdesk_agent.tools.go_client import GoTools, ToolError
from flowdesk_agent.tools.mcp_client import MCPTools
from flowdesk_agent.tools.mcp_server import ACTION_NAME, create_server


def fixture(item):
    action_id, ticket_id = str(uuid4()), str(uuid4())
    context = {
        "run_id": str(item.run_id),
        "tenant_id": str(uuid4()),
        "actor_user_id": str(uuid4()),
        "lease_generation": item.lease_generation,
        "capability": "synthetic-capability",
        "content": "synthetic",
        "message_id": str(uuid4()),
        "deadline_at": "2099-01-01T00:00:00Z",
        "tools": ["order.lookup", "logistics.track", "ticket.search", "ticket.create"],
    }
    receipt = {
        "id": str(uuid4()),
        "action_id": action_id,
        "tool_name": "ticket.create",
        "business_object_type": "ticket",
        "business_object_id": ticket_id,
        "result": {"ticket_no": "T-SYNTHETIC"},
        "executed_at": "2026-11-03T00:00:00Z",
    }
    flags = {"approved": False, "revoked": False, "effects": 0}

    def handle(path, body, headers):
        assert headers.get("authorization") == "Bearer " + "synthetic-service-identity-0000000000"
        if flags["revoked"]:
            return 403, {"code": "PERMISSION_DENIED"}
        if path.endswith("/lease"):
            assert body == {
                "dispatch_id": str(item.dispatch_id),
                "lease_generation": item.lease_generation,
            }
            return 200, {"data": context}
        assert headers.get("x-run-capability") == context["capability"]
        if path == "/internal/v1/tools/order.lookup":
            if body["arguments"]["order_no"] == "FOREIGN-ORDER":
                return 404, {"code": "RESOURCE_NOT_FOUND"}
            return 200, {"code": "OK", "request_id": "synthetic", "data": {"tickets": []}}
        if path.endswith(f"/actions/{action_id}/execute"):
            if not flags["approved"]:
                return 409, {"code": "APPROVAL_REQUIRED"}
            flags["effects"] = 1
            return 200, {"data": receipt}
        return 404, {"code": "RESOURCE_NOT_FOUND"}

    return context, receipt, flags, handle


def test_mcp_protocol_discovery_validation_permissions_and_receipt_replay():
    async def scenario():
        item = command()
        context, receipt, flags, handler = fixture(item)

        async def handle(request):
            status, body = handler(request.url.path, json.loads(request.content), request.headers)
            return httpx.Response(status, json=body)

        async def guard():
            pass

        async with httpx.AsyncClient(
            base_url="https://go.test",
            headers={"Authorization": "Bearer synthetic-service-identity-0000000000"},
            transport=httpx.MockTransport(handle),
        ) as http:
            server = create_server(GoTools(http, item, None, None, guard))
            async with Client(server) as client:
                assert client.protocol_version
                definitions = (await client.list_tools()).tools
                assert {tool.name for tool in definitions} == {*context["tools"][:3], ACTION_NAME}
                for tool in definitions:
                    assert not {"tenant_id", "actor", "approved"}.intersection(
                        tool.input_schema.get("properties", {})
                    )
                for name, args, code in (
                    ("arbitrary.shell", {}, "UNKNOWN_TOOL"),
                    (
                        "order.lookup",
                        {"order_no": "FD0001", "tenant_id": str(uuid4())},
                        "INVALID_ARGUMENT",
                    ),
                    ("order.lookup", {"order_no": "FOREIGN-ORDER"}, "RESOURCE_NOT_FOUND"),
                    (ACTION_NAME, {"action_id": receipt["action_id"]}, "APPROVAL_REQUIRED"),
                ):
                    result = await client.call_tool(name, args)
                    assert result.is_error and result.structured_content == {"code": code}
                assert flags["effects"] == 0
                flags["approved"] = True
                first = await client.call_tool(ACTION_NAME, {"action_id": receipt["action_id"]})
                again = await client.call_tool(ACTION_NAME, {"action_id": receipt["action_id"]})
                assert not first.is_error
                assert first.structured_content == again.structured_content == {"data": receipt}
                assert flags["effects"] == 1
                context["tools"].remove("order.lookup")
                hidden = await client.call_tool("order.lookup", {"order_no": "FD0001"})
                assert hidden.is_error and hidden.structured_content["code"] == "PERMISSION_DENIED"
                context["tools"] = ["ticket.resolve"]
                assert [tool.name for tool in (await client.list_tools()).tools] == [ACTION_NAME]
                flags["revoked"] = True
                revoked = await client.call_tool(ACTION_NAME, {"action_id": receipt["action_id"]})
                assert revoked.is_error
                assert revoked.structured_content["code"] == "CAPABILITY_REVOKED"

    asyncio.run(scenario())


def test_mcp_stdio_subprocess_uses_trusted_run_and_no_model_credentials(monkeypatch):
    item = command()
    _, receipt, flags, handler = fixture(item)
    monkeypatch.setenv("MODEL_API_KEY", "secret-not-for-the-tool-process")

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            status, result = handler(
                self.path, body, {name.lower(): value for name, value in self.headers.items()}
            )
            data = json.dumps(result).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()

    async def scenario():
        tools = MCPTools(
            item,
            f"http://127.0.0.1:{server.server_port}",
            "synthetic-service-identity-0000000000",
        )
        assert "MODEL_API_KEY" not in tools.parameters.env
        assert "AGENT_DATABASE_URL" not in tools.parameters.env
        assert await tools.call("order.lookup", {"order_no": "FD0001"}) == {"tickets": []}
        try:
            await tools.call(ACTION_NAME, {"action_id": receipt["action_id"]})
        except ToolError as exc:
            assert exc.code == "APPROVAL_REQUIRED"
        else:
            raise AssertionError("Unapproved action did not return its exact business failure")
        assert flags["effects"] == 0
        flags["revoked"] = True
        try:
            await tools.call("order.lookup", {"order_no": "FD0001"})
        except ToolError as exc:
            assert exc.code in ("MCP_RESULT_UNKNOWN", "PERMISSION_DENIED")
        else:
            raise AssertionError("revoked authority accepted")

    try:
        asyncio.run(scenario())
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
