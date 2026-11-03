"""Durable workers with a PostgreSQL session lock held by the checkpoint connection."""

import asyncio
import logging
import os
from datetime import UTC, datetime

import httpx
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.types import Command
from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.api.schemas import ExecutionCommand
from flowdesk_agent.graph.read_graph import build_graph
from flowdesk_agent.models import CompatibleModel
from flowdesk_agent.observability.spans import SpanRecorder
from flowdesk_agent.reranking import ModelReranker
from flowdesk_agent.retrieval import Retriever
from flowdesk_agent.storage.callbacks import CallbackStore, DeliveryFailed
from flowdesk_agent.storage.commands import CommandStore
from flowdesk_agent.storage.tool_cache import ToolCache
from flowdesk_agent.tools.go_client import GoTools, OwnershipLost, ToolError
from flowdesk_agent.tools.mcp_client import MCPTools

logger = logging.getLogger(__name__)
MAX_EXECUTIONS = 10
POLL_SECONDS = 0.1


def execution_guard(commands, conn, saver, command, owner):
    async def guard():
        # Saver pipelines run in the background; every query on the execution
        # session shares this mutex, including the ownership fence.
        async with saver.lock:
            if not await commands.still_owned(conn, command.dispatch_id, owner):
                raise OwnershipLost

    return guard


class AgentRuntime:
    def __init__(self, pool: AsyncConnectionPool, http: httpx.AsyncClient):
        self.pool = pool
        self.http = http
        self.commands = CommandStore(pool)
        self.callbacks = CallbackStore(pool)
        self.cache = ToolCache(pool)

    async def callback_loop(self):
        while True:
            try:
                row = await self.callbacks.claim()
                if not row:
                    await asyncio.sleep(POLL_SECONDS)
                    continue
                # Renew authority for fresh events; a terminal duplicate may
                # only need a read-only ack.
                command_row = await self.commands.find(row["dispatch_id"])
                command = ExecutionCommand.model_validate(command_row["body"])
                lease = await self.http.post(
                    f"/internal/v1/runs/{command.run_id}/lease",
                    json={
                        "dispatch_id": str(command.dispatch_id),
                        "lease_generation": command.lease_generation,
                    },
                )
                token = lease.json()["data"]["capability"] if lease.status_code == 200 else ""
                response = await self.http.post(
                    f"/internal/v1/runs/{row['run_id']}/events",
                    json=row["body"],
                    headers={"X-Run-Capability": token},
                )
                if response.status_code == 200:
                    await self.callbacks.acknowledge(row, int(response.json()["data"]["seq"]))
                else:
                    permanent = response.status_code in (400, 401, 403, 404, 409, 410, 422)
                    await self.callbacks.retry(row, "CALLBACK_REJECTED", permanent)
            except asyncio.CancelledError:
                raise
            except Exception:
                # Do not log exceptions containing request headers, credentials or prompts.
                logger.warning("callback delivery deferred")
                if "row" in locals() and row:
                    await self.callbacks.retry(row, "UPSTREAM_UNAVAILABLE")
                await asyncio.sleep(0.5)

    async def execute(self, row: dict):
        command = ExecutionCommand.model_validate(row["body"])
        owner = row["lease_owner"]
        heartbeat_task = None
        locked = False
        try:
            async with self.pool.connection() as conn:
                lock = await (
                    await conn.execute(
                        "SELECT pg_try_advisory_lock(hashtextextended(%s,3)) AS acquired",
                        (str(command.run_id),),
                    )
                ).fetchone()
                if not lock["acquired"]:
                    await self.commands.release(command.dispatch_id, owner, "queued")
                    return
                locked = True

                saver = AsyncPostgresSaver(conn)
                guard = execution_guard(self.commands, conn, saver, command, owner)

                async def heartbeat():
                    while True:
                        await asyncio.sleep(10)
                        if not await self.commands.heartbeat(command.dispatch_id, owner):
                            return

                try:
                    if not await self.commands.own(conn, command, owner):
                        raise OwnershipLost
                    heartbeat_task = asyncio.create_task(heartbeat())
                    terminal = await self.callbacks.terminal(command.dispatch_id)
                    if terminal:
                        await self.callbacks.wait(terminal["source_event_id"])
                        status = "completed" if terminal["type"] == "run.succeeded" else "failed"
                        await self.commands.release(command.dispatch_id, owner, status)
                        return
                    suspended = await self.callbacks.suspended(command.dispatch_id)
                    if suspended:
                        await self.callbacks.wait(suspended["source_event_id"])
                        await self.commands.release(command.dispatch_id, owner, "waiting")
                        return
                    transport = os.getenv("TOOL_TRANSPORT", "http")
                    if transport not in ("http", "mcp"):
                        raise ToolError("TOOL_TRANSPORT_INVALID")
                    mcp = (
                        MCPTools(command, str(self.http.base_url), os.environ["INTERNAL_TOKEN"])
                        if transport == "mcp"
                        else None
                    )
                    tools = GoTools(self.http, command, self.callbacks, self.cache, guard, mcp=mcp)
                    context = await tools.authorize()
                    remaining = min(
                        120.0,
                        (
                            datetime.fromisoformat(context.deadline_at.replace("Z", "+00:00"))
                            - datetime.now(UTC)
                        ).total_seconds(),
                    )
                    async with asyncio.timeout(max(0.01, remaining)):
                        started = await self.callbacks.emit(command, "run.started", {})
                        await self.callbacks.wait(started)
                        # The saver uses this exact session, so losing
                        # the execution lock also loses checkpoint write
                        # access.
                        provider = os.environ.get("MODEL_PROVIDER", "mock")
                        if provider not in ("mock", "openai-compatible"):
                            raise ToolError("MODEL_CONFIG_INVALID")
                        model = CompatibleModel(self.pool, command) if provider != "mock" else None
                        rerank_provider = os.environ.get("RERANK_PROVIDER", "none")
                        if rerank_provider not in ("none", "chat") or (
                            rerank_provider == "chat" and model is None
                        ):
                            raise ToolError("RERANK_CONFIG_INVALID")
                        reranker = (
                            ModelReranker(self.pool, model) if rerank_provider == "chat" else None
                        )
                        graph = build_graph(
                            tools,
                            saver,
                            model,
                            Retriever(self.pool, tools, reranker=reranker),
                            SpanRecorder(self.pool, command),
                        )
                        config = {
                            "configurable": {"thread_id": f"run:{command.run_id}"},
                            "recursion_limit": 40,
                        }
                        checkpoint = await graph.aget_state(config)
                        if not checkpoint.values:
                            await graph.ainvoke({"content": context.content}, config)
                        elif command.command == "resume" and any(
                            task.interrupts for task in checkpoint.tasks
                        ):
                            await graph.ainvoke(Command(resume=context.resume_data), config)
                        elif checkpoint.next:
                            await graph.ainvoke(None, config)
                        checkpoint = await graph.aget_state(config)
                        pending = [item for task in checkpoint.tasks for item in task.interrupts]
                        if pending:
                            value = dict(pending[0].value)
                            kind = value.pop("kind")
                            if kind not in ("input", "approval"):
                                raise ToolError("CHECKPOINT_INVALID")
                            value["checkpoint_ref"] = str(
                                checkpoint.config["configurable"]["checkpoint_id"]
                            )
                            # The saved interrupt precedes its visible wait event.
                            event = await self.callbacks.emit(command, "run.waiting_" + kind, value)
                            await self.callbacks.wait(event)
                            await self.commands.release(command.dispatch_id, owner, "waiting")
                        else:
                            await self.commands.release(command.dispatch_id, owner, "completed")
                finally:
                    if heartbeat_task:
                        heartbeat_task.cancel()
                        await asyncio.gather(heartbeat_task, return_exceptions=True)
                    if locked and not conn.closed:
                        async with saver.lock:
                            await conn.execute(
                                "SELECT pg_advisory_unlock(hashtextextended(%s,3))",
                                (str(command.run_id),),
                            )
        except asyncio.CancelledError:
            await self.commands.release(command.dispatch_id, owner, "queued")
            raise
        except OwnershipLost:
            logger.warning("execution ownership lost")
        except Exception as exc:
            code = (
                exc.code
                if isinstance(exc, ToolError)
                else "EXECUTION_TIMEOUT"
                if isinstance(exc, TimeoutError)
                else "EXECUTION_FAILED"
            )
            logger.warning("execution failed: %s (%s)", code, type(exc).__name__)
            try:
                await self.finish_failed(command, owner, code)
            except Exception:
                logger.warning("terminal recovery deferred")

    async def finish_failed(self, command: ExecutionCommand, owner, code: str):
        # Reacquire the execution lock after an exception. A replaced worker may not
        # publish a failure after losing its checkpoint session or command lease.
        async with self.pool.connection() as conn:
            lock = await (
                await conn.execute(
                    "SELECT pg_try_advisory_lock(hashtextextended(%s,3)) AS acquired",
                    (str(command.run_id),),
                )
            ).fetchone()
            if not lock["acquired"]:
                return
            try:
                if not await self.commands.still_owned(conn, command.dispatch_id, owner):
                    return
                terminal = await self.callbacks.terminal(command.dispatch_id)
                if terminal and terminal["status"] == "acked":
                    await self.commands.release(
                        command.dispatch_id,
                        owner,
                        "completed" if terminal["type"] == "run.succeeded" else "failed",
                    )
                    return
                try:
                    failed = await self.callbacks.emit(command, "run.failed", {"error_code": code})
                    await self.callbacks.wait(failed, timeout=10)
                except (TimeoutError, DeliveryFailed):
                    pass
                await self.commands.release(command.dispatch_id, owner, "failed", code)
            finally:
                await conn.execute(
                    "SELECT pg_advisory_unlock(hashtextextended(%s,3))", (str(command.run_id),)
                )

    async def execution_loop(self):
        running: set[asyncio.Task] = set()
        try:
            while True:
                finished = {task for task in running if task.done()}
                for task in finished:
                    try:
                        task.result()
                    except Exception:
                        logger.warning("execution recovery deferred")
                running -= finished
                if len(running) < MAX_EXECUTIONS:
                    try:
                        row = await self.commands.claim()
                    except Exception:
                        logger.warning("command claim deferred")
                        await asyncio.sleep(0.5)
                        continue
                    if row:
                        running.add(asyncio.create_task(self.execute(row)))
                        continue
                await asyncio.sleep(POLL_SECONDS)
        finally:
            for task in running:
                task.cancel()
            await asyncio.gather(*running, return_exceptions=True)
