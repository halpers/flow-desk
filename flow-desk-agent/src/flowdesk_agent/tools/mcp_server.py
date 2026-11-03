"""Fixed stdio MCP gateway. Go remains the owner of authorization and effects."""

import asyncio
import json
import os
import re
from pathlib import Path
from uuid import UUID

import httpx
from jsonschema import Draft202012Validator, ValidationError
from mcp.server import Server, ServerRequestContext
from mcp.server.stdio import stdio_server
from mcp.types import (
    CallToolRequestParams,
    CallToolResult,
    ListToolsResult,
    PaginatedRequestParams,
    TextContent,
    Tool,
    ToolAnnotations,
)

from flowdesk_agent.api.schemas import ExecutionCommand
from flowdesk_agent.tools.go_client import GoTools, ToolError, argument_validator, result_validator

ACTION_NAME = "actions.execute"
action_schema = json.loads((Path(__file__).parent / "mcp-action.schema.json").read_text())
action_arguments = Draft202012Validator(action_schema)
READ_NAMES = argument_validator.schema["properties"]["tool_name"]["enum"]


def error_result(code: str) -> CallToolResult:
    # Upstream errors are codes only. Never reflect arguments, URLs or exception text.
    safe_code = code if re.fullmatch(r"[A-Z][A-Z0-9_]{0,63}", code) else "MCP_TOOL_FAILED"
    body = {"code": safe_code}
    return CallToolResult(
        is_error=True,
        structured_content=body,
        content=[TextContent(type="text", text=json.dumps(body))],
    )


def create_server(gateway: GoTools) -> Server:
    async def available():
        context = await gateway.authorize()
        names = [name for name in READ_NAMES if name in context.tools]
        if {"ticket.create", "ticket.add_note"}.intersection(context.tools):
            names.append(ACTION_NAME)
        return names

    async def list_tools(
        context: ServerRequestContext, params: PaginatedRequestParams | None
    ) -> ListToolsResult:
        names = await available()
        return ListToolsResult(
            tools=[
                Tool(
                    name=name,
                    description=(
                        "Execute an existing action; Go enforces current approval and idempotency."
                        if name == ACTION_NAME
                        else "Read authorized business facts through Go."
                    ),
                    input_schema=(
                        action_schema
                        if name == ACTION_NAME
                        else argument_validator.schema["properties"]["arguments"]
                    ),
                    annotations=ToolAnnotations(
                        read_only_hint=name != ACTION_NAME,
                        destructive_hint=name == ACTION_NAME,
                        idempotent_hint=True,
                        open_world_hint=False,
                    ),
                )
                for name in names
            ]
        )

    async def call_tool(context: ServerRequestContext, params: CallToolRequestParams):
        try:
            name, arguments = params.name, params.arguments or {}
            if name not in [*READ_NAMES, ACTION_NAME]:
                return error_result("UNKNOWN_TOOL")
            if name == ACTION_NAME:
                action_arguments.validate(arguments)
            else:
                argument_validator.validate({"tool_name": name, "arguments": arguments})
            if name not in await available():
                return error_result("PERMISSION_DENIED")
            if name == ACTION_NAME:
                result = await gateway.execute_action(arguments["action_id"])
            else:
                response = await gateway.http.post(
                    f"/internal/v1/tools/{name}",
                    json={"arguments": arguments},
                    headers={"X-Run-Capability": gateway.context.capability},
                )
                if response.status_code != 200:
                    return error_result(response.json().get("code", "TOOL_FAILED"))
                body = response.json()
                result_validator.validate(body)
                result = body["data"]
            body = {"data": result}
            return CallToolResult(
                structured_content=body,
                content=[TextContent(type="text", text=json.dumps(body, ensure_ascii=False))],
            )
        except ValidationError:
            return error_result("INVALID_ARGUMENT")
        except ToolError as exc:
            return error_result(exc.code)
        except Exception:
            return error_result("MCP_UPSTREAM_UNAVAILABLE")

    return Server("flowdesk-go-tools", on_list_tools=list_tools, on_call_tool=call_tool)


async def main():
    command = ExecutionCommand(
        run_id=UUID(os.environ["FLOWDESK_MCP_RUN_ID"]),
        dispatch_id=UUID(os.environ["FLOWDESK_MCP_DISPATCH_ID"]),
        lease_generation=int(os.environ["FLOWDESK_MCP_GENERATION"]),
        command="execute",
        graph_version="ticket-v1",
    )
    token = os.environ["INTERNAL_TOKEN"]
    if len(token) < 32:
        raise RuntimeError("service identity is required")

    async def guard():
        # The parent guards ownership; every server request also asks Go for a current lease.
        return None

    async with httpx.AsyncClient(
        base_url=os.environ["GO_BASE_URL"],
        headers={"Authorization": "Bearer " + token},
        timeout=5,
        trust_env=False,
    ) as http:
        gateway = GoTools(http, command, None, None, guard)
        server = create_server(gateway)
        async with stdio_server() as (reader, writer):
            await server.run(reader, writer, server.create_initialization_options())


if __name__ == "__main__":
    asyncio.run(main())
