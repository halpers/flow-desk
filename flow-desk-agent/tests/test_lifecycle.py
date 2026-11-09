import asyncio
from contextlib import asynccontextmanager
from typing import TypedDict
from uuid import uuid4

import httpx
import pytest
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.graph import END, START, StateGraph
from langgraph.types import Command, interrupt
from pydantic import ValidationError
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.api.main import app
from flowdesk_agent.api.schemas import MAX_LIFECYCLE_COUNT, AgentLifecycleData
from flowdesk_agent.observability.operations import bind_scope
from flowdesk_agent.storage.callbacks import CallbackStore
from flowdesk_agent.storage.commands import CommandStore
from flowdesk_agent.storage.lifecycle import LIFECYCLE_COUNTS_SQL, read_lifecycle

COUNTS = {field for field in AgentLifecycleData.model_fields if field.endswith("_count")}


def test_lifecycle_contract_is_required_strict_and_metadata_only():
    values = {
        "available": False,
        "observed_at": "2026-11-06T00:00:00+00:00",
        "observation_only": True,
        "protection_reasons": ["AGENT_SCOPE_UNAVAILABLE"],
        **dict.fromkeys(COUNTS, 0),
    }
    assert AgentLifecycleData.model_validate(values).observation_only
    schema = AgentLifecycleData.model_json_schema()
    assert set(schema["required"]) == set(AgentLifecycleData.model_fields)
    for updates in (
        {"checkpoint": {"private": "SYNTHETIC_PRIVATE_STATE"}},
        {"command_count": -1},
        {"command_count": MAX_LIFECYCLE_COUNT + 1},
        {"command_count": "1"},
        {"command_count": 1.5},
        {"command_count": True},
        {"available": "true"},
        {"observation_only": False},
        {"protection_reasons": ["SAFE_TO_DELETE"]},
    ):
        with pytest.raises(ValidationError):
            AgentLifecycleData.model_validate(values | updates)
    for field in AgentLifecycleData.model_fields:
        partial = values.copy()
        partial.pop(field)
        with pytest.raises(ValidationError):
            AgentLifecycleData.model_validate(partial)


def test_missing_foreign_and_unknown_scopes_do_not_bind_or_expose_counts():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant, other = command(), uuid4(), uuid4()
            await CommandStore(pool).accept(item)
            # Even an actual command does not establish tenant access by itself.
            unavailable = await read_lifecycle(pool, item.run_id, tenant)
            assert not unavailable.available
            assert unavailable.protection_reasons == ["AGENT_SCOPE_UNAVAILABLE"]
            assert all(getattr(unavailable, field) == 0 for field in COUNTS)
            async with pool.connection() as conn:
                binding = await (
                    await conn.execute("SELECT count(*) AS value FROM run_observation_scopes")
                ).fetchone()
                assert binding["value"] == 0
            await bind_scope(pool, item.run_id, tenant)
            for run_id, scope in ((item.run_id, other), (uuid4(), tenant)):
                hidden = await read_lifecycle(pool, run_id, scope)
                assert not hidden.available and hidden.observation_only
                assert hidden.protection_reasons == ["AGENT_SCOPE_UNAVAILABLE"]
                assert all(getattr(hidden, field) == 0 for field in COUNTS)
            visible = await read_lifecycle(pool, item.run_id, tenant)
            assert visible.available and visible.command_count == 1
            assert visible.protection_reasons == ["COMMAND_QUEUED"]

    asyncio.run(scenario())


@pytest.mark.parametrize(
    ("status", "reason"),
    [
        ("queued", "COMMAND_QUEUED"),
        ("running", "COMMAND_RUNNING"),
        ("waiting", "COMMAND_WAITING"),
        ("completed", ""),
        ("failed", ""),
        ("cancelled", ""),
    ],
)
def test_every_command_state_is_counted_without_treating_terminal_as_deletion_permission(
    status, reason
):
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = command(), uuid4()
            await CommandStore(pool).accept(item)
            await bind_scope(pool, item.run_id, tenant)
            async with pool.connection() as conn:
                await conn.execute(
                    "UPDATE execution_commands SET status=%s WHERE dispatch_id=%s",
                    (status, item.dispatch_id),
                )
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert result.command_count == 1
            assert result.queued_command_count == int(status == "queued")
            assert result.running_command_count == int(status == "running")
            assert result.waiting_command_count == int(status == "waiting")
            assert result.protection_reasons == ([reason] if reason else [])
            assert result.observation_only
            assert "can_delete" not in result.model_dump()

    asyncio.run(scenario())


@pytest.mark.parametrize("status", ["pending", "delivering", "acked", "permanent_failure"])
def test_callback_failures_remain_unacknowledged_and_protected(status):
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = command(), uuid4()
            await CommandStore(pool).accept(item)
            await bind_scope(pool, item.run_id, tenant)
            source = await CallbackStore(pool).emit(
                item, "run.succeeded", {"private": "SYNTHETIC_PRIVATE_CALLBACK"}
            )
            async with pool.connection() as conn:
                await conn.execute("UPDATE execution_commands SET status='completed'")
                await conn.execute(
                    "UPDATE callback_outbox SET status=%s WHERE source_event_id=%s",
                    (status, source),
                )
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert result.callback_count == 1
            assert result.unacknowledged_callback_count == int(status != "acked")
            assert result.protection_reasons == (
                ["CALLBACK_UNACKNOWLEDGED"] if status != "acked" else []
            )
            assert "SYNTHETIC_PRIVATE" not in result.model_dump_json()

    asyncio.run(scenario())


@pytest.mark.parametrize("future", [False, True])
def test_live_leases_are_reported_even_when_command_and_callback_are_terminal(future):
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = command(), uuid4()
            await CommandStore(pool).accept(item)
            await bind_scope(pool, item.run_id, tenant)
            source = await CallbackStore(pool).emit(item, "run.succeeded", {})
            seconds = 60 if future else -60
            async with pool.connection() as conn:
                await conn.execute(
                    "UPDATE execution_commands SET status='completed',"
                    "lease_expires_at=now()+%s*interval '1 second' WHERE dispatch_id=%s",
                    (seconds, item.dispatch_id),
                )
                await conn.execute(
                    "UPDATE callback_outbox SET status='acked',"
                    "lease_expires_at=now()+%s*interval '1 second' WHERE source_event_id=%s",
                    (seconds, source),
                )
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert (
                result.live_command_lease_count == result.live_callback_lease_count == int(future)
            )
            assert result.protection_reasons == (
                ["COMMAND_LEASE_ACTIVE", "CALLBACK_LEASE_ACTIVE"] if future else []
            )

    asyncio.run(scenario())


@pytest.mark.parametrize(
    ("status", "reason"),
    [
        ("reserved", "MODEL_RESERVED"),
        ("completed", ""),
        ("failed", ""),
        ("future-unknown-state", "MODEL_STATE_UNKNOWN"),
    ],
)
def test_reserved_or_unknown_model_state_preserves_runtime_protection(status, reason):
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = command(), uuid4()
            await bind_scope(pool, item.run_id, tenant)
            async with pool.connection() as conn:
                await conn.execute(
                    "INSERT INTO model_calls(call_id,run_id,request_hash,model,profile,"
                    "reserved_units,output,status) VALUES(%s,%s,'private','private','private',1,"
                    '\'{"private":"SYNTHETIC_PRIVATE_MODEL_OUTPUT"}\',%s)',
                    (uuid4(), item.run_id, status),
                )
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert result.model_call_count == 1
            assert result.reserved_model_call_count == int(status == "reserved")
            assert result.protection_reasons == ([reason] if reason else [])
            assert "SYNTHETIC_PRIVATE" not in result.model_dump_json()

    asyncio.run(scenario())


async def fingerprint(pool):
    # Synthetic isolated-schema evidence only. Production code never reads payloads.
    tables = (
        "execution_commands",
        "callback_outbox",
        "tool_results",
        "model_calls",
        "checkpoints",
        "checkpoint_blobs",
        "checkpoint_writes",
        "embedding_calls",
        "retrieval_attempts",
        "execution_spans",
        "run_observation_scopes",
    )
    result = {}
    async with pool.connection() as conn:
        for table in tables:
            result[table] = await (
                await conn.execute(
                    "SELECT count(*) AS count, md5(coalesce(string_agg("
                    "md5(to_jsonb(t)::text),'' ORDER BY md5(to_jsonb(t)::text)),'')) AS digest "
                    f"FROM {table} t"
                )
            ).fetchone()
    return result


def test_exact_checkpoint_thread_counts_leave_payloads_and_real_interrupt_recovery_unchanged():
    class State(TypedDict, total=False):
        private: str
        confirmed: str

    async def wait_input(state):
        return {"confirmed": interrupt("SYNTHETIC_PRIVATE_QUESTION")}

    async def scenario():
        async with isolated_pool() as pool, pool.connection() as conn:
            item, tenant = command(), uuid4()
            await CommandStore(pool).accept(item)
            await bind_scope(pool, item.run_id, tenant)
            await conn.execute("UPDATE execution_commands SET status='waiting'")
            builder = StateGraph(State)
            builder.add_node("wait_input", wait_input)
            builder.add_edge(START, "wait_input")
            builder.add_edge("wait_input", END)
            graph = builder.compile(checkpointer=AsyncPostgresSaver(conn))
            thread = "run:" + str(item.run_id)
            config = {"configurable": {"thread_id": thread}}
            await graph.ainvoke({"private": "SYNTHETIC_PRIVATE_CHECKPOINT"}, config)
            paused = await graph.aget_state(config)
            assert paused.tasks and paused.tasks[0].interrupts
            for other in (thread + ":child", "prefix:" + thread, "run:" + str(uuid4())):
                await conn.execute(
                    "INSERT INTO checkpoints(thread_id,checkpoint_ns,checkpoint_id,checkpoint) "
                    "VALUES(%s,'','synthetic-excluded','{}')",
                    (other,),
                )
                await conn.execute(
                    "INSERT INTO checkpoint_blobs(thread_id,channel,version,type,blob) "
                    "VALUES(%s,'private','1','bytes',%s)",
                    (other, b"SYNTHETIC_PRIVATE_BLOB"),
                )
                await conn.execute(
                    "INSERT INTO checkpoint_writes(thread_id,checkpoint_id,task_id,idx,"
                    "channel,type,blob) VALUES(%s,'synthetic-excluded','synthetic',0,'private',"
                    "'bytes',%s)",
                    (other, b"SYNTHETIC_PRIVATE_WRITE"),
                )
            expected = {}
            for table in ("checkpoints", "checkpoint_blobs", "checkpoint_writes"):
                expected[table] = (
                    await (
                        await conn.execute(
                            f"SELECT count(*) AS value FROM {table} WHERE thread_id=%s", (thread,)
                        )
                    ).fetchone()
                )["value"]
                assert expected[table] > 0
            before = await fingerprint(pool)
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert result.checkpoint_count == expected["checkpoints"]
            assert result.checkpoint_blob_count == expected["checkpoint_blobs"]
            assert result.checkpoint_write_count == expected["checkpoint_writes"]
            assert result.protection_reasons == ["COMMAND_WAITING"]
            assert await fingerprint(pool) == before
            assert "SYNTHETIC_PRIVATE" not in result.model_dump_json()
            again = await graph.aget_state(config)
            assert again.values == paused.values and again.tasks == paused.tasks
            resumed = await graph.ainvoke(Command(resume="synthetic-confirmed"), config)
            assert resumed["confirmed"] == "synthetic-confirmed"
            assert resumed["private"] == "SYNTHETIC_PRIVATE_CHECKPOINT"

    asyncio.run(scenario())


def test_misc_counts_are_run_scoped_and_query_embedding_counts_are_tenant_scoped():
    async def scenario():
        async with isolated_pool() as pool:
            item, other, tenant, foreign = command(), command(), uuid4(), uuid4()
            commands = CommandStore(pool)
            await commands.accept(item)
            await commands.accept(other)
            await bind_scope(pool, item.run_id, tenant)
            async with pool.connection() as conn:
                await conn.execute("UPDATE execution_commands SET status='completed'")
                for current in (item, other):
                    await conn.execute(
                        "INSERT INTO tool_results(id,dispatch_id,tool_name,arguments,result) "
                        "VALUES(%s,%s,'SYNTHETIC_PRIVATE_TOOL','{}',"
                        '\'{"private":"SYNTHETIC_PRIVATE_TOOL_RESULT"}\')',
                        (uuid4(), current.dispatch_id),
                    )
                    await conn.execute(
                        "INSERT INTO execution_spans(span_id,run_id,dispatch_id,lease_generation,"
                        "node_name,status,duration_ms) VALUES(%s,%s,%s,1,'finalize','completed',1)",
                        (uuid4(), current.run_id, current.dispatch_id),
                    )
                    await conn.execute(
                        "INSERT INTO retrieval_attempts(attempt_id,run_id,profile,status,"
                        "candidate_count,duration_ms) VALUES(%s,%s,'private','completed',1,1)",
                        (uuid4(), current.run_id),
                    )
                for scope, operation, status in (
                    (tenant, "query:" + str(item.run_id), "reserved"),
                    (tenant, "query:" + str(item.run_id), "completed"),
                    (tenant, "query:" + str(item.run_id), "failed"),
                    (foreign, "query:" + str(item.run_id), "reserved"),
                    (tenant, "query:" + str(other.run_id), "reserved"),
                    (tenant, "index:" + str(item.run_id), "reserved"),
                ):
                    await conn.execute(
                        "INSERT INTO embedding_calls(call_id,tenant_id,profile,operation,"
                        "request_hash,reserved_bytes,status) "
                        "VALUES(%s,%s,'private',%s,'private',1,%s)",
                        (uuid4(), scope, operation, status),
                    )
            before = await fingerprint(pool)
            result = await read_lifecycle(pool, item.run_id, tenant)
            assert (
                result.command_count == result.tool_result_count == result.execution_span_count == 1
            )
            assert result.retrieval_attempt_count == 1
            assert result.embedding_call_count == 3 and result.reserved_embedding_call_count == 1
            assert result.protection_reasons == ["EMBEDDING_RESERVED"]
            assert "SYNTHETIC_PRIVATE" not in result.model_dump_json()
            assert await fingerprint(pool) == before

    asyncio.run(scenario())


def test_lifecycle_counts_share_one_readonly_snapshot_and_do_not_persist_timeout_settings():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = command(), uuid4()
            await bind_scope(pool, item.run_id, tenant)

            class Connection:
                def __init__(self, conn):
                    self.conn = conn

                def transaction(self):
                    return self.conn.transaction()

                async def execute(self, query, params=None):
                    if query == LIFECYCLE_COUNTS_SQL:
                        for setting, expected in (
                            ("transaction_read_only", "on"),
                            ("transaction_isolation", "repeatable read"),
                            ("statement_timeout", "2500ms"),
                        ):
                            row = await (
                                await self.conn.execute(
                                    "SELECT current_setting(%s) AS value", (setting,)
                                )
                            ).fetchone()
                            assert row["value"] == expected
                        async with pool.connection() as writer:
                            await writer.execute(
                                "INSERT INTO model_calls(call_id,run_id,request_hash,model,profile,"
                                "reserved_units,status) VALUES(%s,%s,'private','private','private',"
                                "1,'reserved')",
                                (uuid4(), item.run_id),
                            )
                    return await self.conn.execute(query, params)

            class Pool:
                @asynccontextmanager
                async def connection(self):
                    async with pool.connection() as conn:
                        yield Connection(conn)
                        settings = await (
                            await conn.execute(
                                "SELECT current_setting('statement_timeout') AS timeout,"
                                "current_setting('transaction_read_only') AS read_only"
                            )
                        ).fetchone()
                        assert settings == {"timeout": "0", "read_only": "off"}

            first = await read_lifecycle(Pool(), item.run_id, tenant)
            assert first.model_call_count == first.reserved_model_call_count == 0
            second = await read_lifecycle(pool, item.run_id, tenant)
            assert second.model_call_count == second.reserved_model_call_count == 1
            assert second.protection_reasons == ["MODEL_RESERVED"]

    asyncio.run(scenario())


def test_lifecycle_http_requires_service_identity_and_valid_scopes_and_never_returns_payload(
    monkeypatch,
):
    async def scenario():
        token = "synthetic-lifecycle-service-token-000000"
        monkeypatch.setenv("INTERNAL_TOKEN", token)
        async with isolated_pool() as pool:
            monkeypatch.setattr(app.state, "pool", pool, raising=False)
            item, tenant = command(), uuid4()
            await CommandStore(pool).accept(item)
            await bind_scope(pool, item.run_id, tenant)
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=app), base_url="http://agent"
            ) as client:
                path = f"/internal/v1/runs/{item.run_id}/lifecycle?tenant_id={tenant}"
                assert (await client.get(path)).status_code == 401
                client.headers["Authorization"] = "Bearer wrong-service-token"
                assert (await client.get(path)).status_code == 401
                client.headers["Authorization"] = f"Bearer {token}"
                response = await client.get(path)
                assert response.status_code == 200
                assert response.headers["Cache-Control"] == "no-store"
                data = response.json()
                assert data["available"] and data["observation_only"]
                assert data["command_count"] == 1
                assert set(data) == set(AgentLifecycleData.model_fields)
                assert (await client.get(path.replace(str(tenant), "bad"))).status_code == 422
                assert (await client.get(path.replace(str(item.run_id), "bad"))).status_code == 422
                assert (await client.get(path.split("?")[0])).status_code == 422
                hidden = await client.get(path.replace(str(tenant), str(uuid4())))
                assert hidden.status_code == 200 and not hidden.json()["available"]
                assert all(hidden.json()[field] == 0 for field in COUNTS)

    asyncio.run(scenario())
