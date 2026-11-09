"""Disposable-schema tests for lossless history movement and permanent fencing."""

import asyncio
from uuid import uuid4

import httpx
import psycopg
import pytest
from psycopg import sql
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.api.main import app
from flowdesk_agent.observability.operations import bind_scope
from flowdesk_agent.storage.callbacks import CallbackStore
from flowdesk_agent.storage.commands import CommandStore
from flowdesk_agent.storage.history import TABLES, HistoryCommand, change_history
from flowdesk_agent.storage.lifecycle import read_lifecycle
from flowdesk_agent.tools.go_client import ToolError


async def seed_history(pool):
    item, tenant = command(), uuid4()
    await CommandStore(pool).accept(item)
    await bind_scope(pool, item.run_id, tenant)
    thread = "run:" + str(item.run_id)
    async with pool.connection() as conn:
        await conn.execute("UPDATE execution_commands SET status='completed'")
        await conn.execute(
            "INSERT INTO checkpoints(thread_id,checkpoint_id,checkpoint,metadata) "
            "VALUES(%s,'synthetic-checkpoint','{\"v\":1}','{\"source\":\"synthetic\"}')",
            (thread,),
        )
        await conn.execute(
            "INSERT INTO checkpoint_blobs(thread_id,channel,version,type,blob) "
            "VALUES(%s,'answer','1','bytes',%s)",
            (thread, b"\x00\xffsynthetic checkpoint bytes\x00"),
        )
        await conn.execute(
            "INSERT INTO checkpoint_writes(thread_id,checkpoint_id,task_id,idx,channel,type,blob) "
            "VALUES(%s,'synthetic-checkpoint','task',0,'answer','bytes',%s)",
            (thread, b"\x00\xfeunmodified write\x00"),
        )
    return item, tenant


async def snapshot(pool, run_id, cold=False):
    result = {}
    async with pool.connection() as conn:
        for table in TABLES:
            result[table] = await (
                await conn.execute(
                    sql.SQL("SELECT * FROM {} WHERE thread_id=%s").format(
                        sql.Identifier(table + "_history" if cold else table)
                    ),
                    ("run:" + str(run_id),),
                )
            ).fetchall()
    return result


def history_command(tenant, generation=1, target="archived"):
    return HistoryCommand(
        operation_id=uuid4(), tenant_id=tenant, generation=generation, target=target
    )


def test_history_preserves_bytes_counts_original_receipts_and_fence_after_restore():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = await seed_history(pool)
            original = await snapshot(pool, item.run_id)
            archive = history_command(tenant)
            receipt = await change_history(pool, item.run_id, archive)
            assert (
                receipt.checkpoint_count,
                receipt.checkpoint_blob_count,
                receipt.checkpoint_write_count,
            ) == (1, 1, 1)
            assert all(not rows for rows in (await snapshot(pool, item.run_id)).values())
            assert await snapshot(pool, item.run_id, cold=True) == original
            observed = await read_lifecycle(pool, item.run_id, tenant)
            assert observed.checkpoint_count == observed.checkpoint_blob_count == 1
            assert observed.checkpoint_write_count == 1 and not observed.protection_reasons
            # A replacement process retries exactly the original durable command.
            assert await change_history(pool, item.run_id, archive) == receipt
            restore = history_command(tenant, 2, "live")
            restored = await change_history(pool, item.run_id, restore)
            assert restored.generation == 2 and await snapshot(pool, item.run_id) == original
            assert all(not rows for rows in (await snapshot(pool, item.run_id, True)).values())
            # Replaying an old request never rearchives data after a newer restore.
            assert await change_history(pool, item.run_id, archive) == receipt
            assert await snapshot(pool, item.run_id) == original
            with pytest.raises(psycopg.errors.CheckViolation, match="RUN_HISTORY_FENCED"):
                await CommandStore(pool).accept(item)
            with pytest.raises(psycopg.errors.CheckViolation, match="RUN_HISTORY_FENCED"):
                await CallbackStore(pool).emit(item, "run.started", {})
            async with pool.connection() as conn:
                with pytest.raises(psycopg.errors.CheckViolation, match="RUN_HISTORY_FENCED"):
                    await conn.execute(
                        "UPDATE checkpoint_blobs SET blob=%s WHERE thread_id=%s",
                        (b"late old worker", "run:" + str(item.run_id)),
                    )
            async with pool.connection() as conn:
                assert not await CommandStore(pool).still_owned(conn, item.dispatch_id, uuid4())
                assert not await CommandStore(pool).own(conn, item, uuid4())
                with pytest.raises(psycopg.errors.CheckViolation, match="IMMUTABLE"):
                    await conn.execute("DELETE FROM run_history_receipts")
                await conn.execute("UPDATE execution_commands SET status='queued'")
            assert await CommandStore(pool).claim() is None

    asyncio.run(scenario())


def test_history_http_requires_service_identity_and_validates_maintenance_envelope(monkeypatch):
    async def scenario():
        token = "synthetic-history-service-token-000000"
        monkeypatch.setenv("INTERNAL_TOKEN", token)
        async with isolated_pool() as pool:
            monkeypatch.setattr(app.state, "pool", pool, raising=False)
            item, tenant = await seed_history(pool)
            body = history_command(tenant).model_dump(mode="json")
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=app), base_url="http://agent"
            ) as client:
                path = f"/internal/v1/runs/{item.run_id}/history"
                assert (await client.post(path, json=body)).status_code == 401
                client.headers["Authorization"] = "Bearer wrong-service-token"
                assert (await client.post(path, json=body)).status_code == 401
                client.headers["Authorization"] = f"Bearer {token}"
                for invalid in (
                    body | {"generation": True},
                    body | {"target": "purged"},
                    body | {"checkpoint": "untrusted"},
                ):
                    assert (await client.post(path, json=invalid)).status_code == 422
                response = await client.post(path, json=body)
                assert response.status_code == 200
                assert response.headers["cache-control"] == "no-store"
                assert response.json()["operation_id"] == body["operation_id"]
                assert set(response.json()) == {
                    "operation_id",
                    "run_id",
                    "tenant_id",
                    "generation",
                    "target",
                    "checkpoint_count",
                    "checkpoint_blob_count",
                    "checkpoint_write_count",
                }

    asyncio.run(scenario())


def test_history_atomic_rollback_and_recovery_after_late_failure():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = await seed_history(pool)
            original = await snapshot(pool, item.run_id)
            request = history_command(tenant)
            async with pool.connection() as conn:
                await conn.execute(
                    "CREATE FUNCTION synthetic_fail_receipt() RETURNS trigger LANGUAGE plpgsql "
                    "AS $$ BEGIN RAISE EXCEPTION 'synthetic crash after tier movement'; END $$"
                )
                await conn.execute(
                    "CREATE TRIGGER synthetic_fail_receipt BEFORE INSERT ON run_history_receipts "
                    "FOR EACH ROW EXECUTE FUNCTION synthetic_fail_receipt()"
                )
            with pytest.raises(psycopg.errors.RaiseException, match="synthetic crash"):
                await change_history(pool, item.run_id, request)
            assert await snapshot(pool, item.run_id) == original
            assert all(not rows for rows in (await snapshot(pool, item.run_id, True)).values())
            async with pool.connection() as conn:
                assert not await (await conn.execute("SELECT 1 FROM run_history_fences")).fetchone()
                await conn.execute("DROP TRIGGER synthetic_fail_receipt ON run_history_receipts")
            recovered = await change_history(pool, item.run_id, request)
            assert await change_history(pool, item.run_id, request) == recovered
            assert await snapshot(pool, item.run_id, True) == original

    asyncio.run(scenario())


def test_history_scope_generation_quiescence_and_exact_executor_lock():
    async def scenario():
        async with isolated_pool() as pool:
            item, tenant = await seed_history(pool)
            for run_id, request, reason in (
                (item.run_id, history_command(uuid4()), "SCOPE_UNAVAILABLE"),
                (uuid4(), history_command(tenant), "SCOPE_UNAVAILABLE"),
                (item.run_id, history_command(tenant, 2), "CONFLICT"),
            ):
                with pytest.raises(ToolError, match=reason):
                    await change_history(pool, run_id, request)
            async with pool.connection() as owner:
                await owner.execute(
                    "SELECT pg_advisory_lock(hashtextextended(%s,3))", (str(item.run_id),)
                )
                try:
                    with pytest.raises(ToolError, match="EXECUTOR_ACTIVE"):
                        await change_history(pool, item.run_id, history_command(tenant))
                finally:
                    await owner.execute(
                        "SELECT pg_advisory_unlock(hashtextextended(%s,3))", (str(item.run_id),)
                    )
            async with pool.connection() as conn:
                await conn.execute("UPDATE execution_commands SET status='waiting'")
            with pytest.raises(ToolError, match="PROTECTED"):
                await change_history(pool, item.run_id, history_command(tenant))
            assert all(len(rows) == 1 for rows in (await snapshot(pool, item.run_id)).values())

    asyncio.run(scenario())
