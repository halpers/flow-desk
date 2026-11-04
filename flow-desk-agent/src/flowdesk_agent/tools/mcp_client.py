"""A fixed, bounded MCP subprocess per invocation, with no model-selected commands."""

import asyncio
import os
import sys
from pathlib import Path

from mcp import Client
from mcp.client.stdio import StdioServerParameters, stdio_client

from flowdesk_agent.api.schemas import ExecutionCommand
from flowdesk_agent.observability.telemetry import current_headers
from flowdesk_agent.tools.go_client import ToolError

MCP_TIMEOUT_SECONDS = 20


class MCPTools:
    def __init__(self, command: ExecutionCommand, base_url: str, token: str):
        # No database credentials, model keys, proxy settings or user command reach the child.
        environment = {
            name: value
            for name in ("SYSTEMROOT", "TEMP", "TMP", "PATH")
            if (value := os.environ.get(name)) is not None
        }
        environment.update(
            {
                "PYTHONPATH": str(Path(__file__).resolve().parents[2]),
                "PYTHONIOENCODING": "utf-8",
                "GO_BASE_URL": base_url,
                "INTERNAL_TOKEN": token,
                "FLOWDESK_MCP_RUN_ID": str(command.run_id),
                "FLOWDESK_MCP_DISPATCH_ID": str(command.dispatch_id),
                "FLOWDESK_MCP_GENERATION": str(command.lease_generation),
                "FLOWDESK_MCP_TRACEPARENT": current_headers().get("traceparent", ""),
            }
        )
        self.parameters = StdioServerParameters(
            command=sys.executable,
            args=["-m", "flowdesk_agent.tools.mcp_server"],
            env=environment,
        )

    async def call(self, name: str, arguments: dict):
        try:
            async with asyncio.timeout(MCP_TIMEOUT_SECONDS):
                # Child errors must never leak service credentials to logs.
                with open(os.devnull, "w", encoding="utf-8") as errors:
                    parameters = self.parameters.model_copy(
                        update={
                            "env": {
                                **self.parameters.env,
                                "FLOWDESK_MCP_TRACEPARENT": current_headers().get(
                                    "traceparent", ""
                                ),
                            }
                        }
                    )
                    async with Client(stdio_client(parameters, errlog=errors)) as client:
                        definitions = (await client.list_tools()).tools
                        if name not in {tool.name for tool in definitions}:
                            raise ToolError("PERMISSION_DENIED")
                        result = await client.call_tool(name, arguments)
                        body = result.structured_content
                        if not isinstance(body, dict):
                            raise ToolError("MCP_INVALID_RESULT")
                        if result.is_error:
                            code = body.get("code")
                            raise ToolError(code if isinstance(code, str) else "MCP_TOOL_FAILED")
                        if "data" not in body:
                            raise ToolError("MCP_INVALID_RESULT")
                        return body["data"]
        except ToolError:
            raise
        except Exception:
            # An uncertain write is retried only through the exact same action_id.
            raise ToolError("MCP_RESULT_UNKNOWN") from None
