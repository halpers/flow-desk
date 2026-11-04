import asyncio
from uuid import uuid4

import httpx
import pytest
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.api.main import app
from flowdesk_agent.observability.operations import bind_scope, read_trace
from flowdesk_agent.storage.commands import CommandStore
from flowdesk_agent.tools.go_client import ToolError


def test_observation_scope_redaction_unknown_usage_and_totals():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant, other = command(), uuid4(), uuid4()
            await CommandStore(pool).accept(item)
            assert not (await read_trace(pool, item.run_id, tenant)).available
            await bind_scope(pool, item.run_id, tenant)
            await bind_scope(pool, item.run_id, tenant)
            with pytest.raises(ToolError):
                await bind_scope(pool, item.run_id, other)
            async with pool.connection() as conn:
                for index in range(102):
                    await conn.execute(
                        "INSERT INTO model_calls(call_id,run_id,request_hash,model,profile,"
                        "reserved_units,input_tokens,output_tokens,output,status,error_code) "
                        "VALUES(%s,%s,'PRIVATE_HASH','PRIVATE_MODEL','private',1,%s,2,"
                        "'{\"secret\":\"PRIVATE_OUTPUT\"}','failed','PRIVATE_ERROR')",
                        (uuid4(), item.run_id, None if index == 101 else 3),
                    )
                await conn.execute(
                    "INSERT INTO execution_spans "
                    "VALUES(%s,%s,%s,1,'PRIVATE_NODE','failed',5,now())",
                    (uuid4(), item.run_id, item.dispatch_id),
                )
                await conn.execute(
                    "INSERT INTO tool_results VALUES(%s,%s,'PRIVATE_TOOL',"
                    '\'{"secret":"PRIVATE_ARGUMENTS"}\',\'{"secret":"PRIVATE_FACT"}\',now())',
                    (uuid4(), item.dispatch_id),
                )
                # Foreign-tenant embedding usage with the same operation must not be attributed.
                for scope, operation in (
                    (tenant, "index:private"),
                    (other, "query:" + str(item.run_id)),
                ):
                    await conn.execute(
                        "INSERT INTO embedding_calls(call_id,tenant_id,profile,operation,"
                        "request_hash,reserved_bytes,status,input_tokens) "
                        "VALUES(%s,%s,'private',%s,'private',1,'completed',999)",
                        (uuid4(), scope, operation),
                    )
            trace = await read_trace(pool, item.run_id, tenant)
            assert trace.available and trace.truncated and len(trace.models) == 100
            assert trace.usage.model_attempts == 102
            assert trace.usage.input_tokens == 303 and trace.usage.output_tokens == 204
            assert not trace.usage.model_tokens_known and trace.usage.cost_status == "unknown"
            assert trace.usage.embedding_attempts == 0
            assert not trace.models[0].duration_known
            assert trace.nodes[0].node == "unknown" and trace.tools[0].tool == "unknown"
            assert "PRIVATE" not in trace.model_dump_json()
            assert not (await read_trace(pool, item.run_id, other)).available
            assert not (await read_trace(pool, uuid4(), tenant)).available

    asyncio.run(scenario())


def test_observation_http_requires_service_identity_and_valid_identifiers(monkeypatch):
    async def scenario():
        monkeypatch.setenv("INTERNAL_TOKEN", "test-observation-service-token-0000")
        async with isolated_pool() as pool:
            app.state.pool = pool
            item, tenant = command(), uuid4()
            await bind_scope(pool, item.run_id, tenant)
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=app), base_url="http://agent"
            ) as client:
                path = f"/internal/v1/runs/{item.run_id}/trace?tenant_id={tenant}"
                assert (await client.get(path)).status_code == 401
                client.headers["Authorization"] = "Bearer test-observation-service-token-0000"
                response = await client.get(path)
                assert response.status_code == 200 and response.json()["available"]
                assert (await client.get(path.replace(str(tenant), "bad"))).status_code == 422
                wrong = await client.get(path.replace(str(tenant), str(uuid4())))
                assert wrong.status_code == 200 and not wrong.json()["available"]

    asyncio.run(scenario())
