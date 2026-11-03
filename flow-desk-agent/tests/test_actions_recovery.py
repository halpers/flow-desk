import asyncio
from types import SimpleNamespace
from uuid import uuid4

import pytest
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.types import Command
from psycopg import OperationalError
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.api.schemas import CancelCommand
from flowdesk_agent.graph.read_graph import FakeModel, build_graph
from flowdesk_agent.storage.commands import CommandStore
from flowdesk_agent.tools.go_client import ToolError


class DurableTools:
    """Go effects are separately tested against PostgreSQL in the Go suite."""

    def __init__(self, item):
        self.command = item
        self.callbacks = self
        self.allocated = []
        self.effects = {}
        self.events = []
        self.prepare_crash = True
        self.execute_crash = True
        self.order_id = str(uuid4())
        self.ticket_id = str(uuid4())
        self.message_id = uuid4()

    async def authorize(self):
        return SimpleNamespace(message_id=self.message_id)

    async def guard(self):
        pass

    async def call(self, tool, arguments):
        if tool == "order.lookup":
            return {
                "order": {
                    "id": self.order_id,
                    "order_no": arguments["order_no"],
                    "status": "in_transit",
                    "version": 1,
                    "masked_customer_name": "合成*",
                    "total_amount": "19.99",
                    "currency": "CNY",
                }
            }
        return {"logistics": [], "tickets": []}

    async def prepare_action(self, action_id, tool, arguments, evidence_ids):
        self.allocated.append(action_id)
        if self.prepare_crash:
            self.prepare_crash = False
            raise RuntimeError("process stopped after prepare response")
        return {"id": str(uuid4()), "action_id": action_id}

    async def execute_action(self, action_id):
        self.effects.setdefault(
            action_id,
            {
                "action_id": action_id,
                "tool_name": "ticket.create",
                "business_object_type": "ticket",
                "business_object_id": self.ticket_id,
                "result": {"ticket_no": "T-SYNTHETIC"},
            },
        )
        if self.execute_crash:
            self.execute_crash = False
            raise RuntimeError("process stopped after business COMMIT")
        return self.effects[action_id]

    async def emit(self, item, kind, payload):
        self.events.append((kind, payload))
        return uuid4()

    async def wait(self, event):
        pass


class FailedSummary(FakeModel):
    async def answer(self, state):
        raise ToolError("MODEL_OUTPUT_INVALID")


def test_checkpoint_recovers_same_action_and_receipt_after_two_crashes():
    async def scenario():
        async with isolated_pool() as pool:
            tools = DurableTools(command())
            config = {"configurable": {"thread_id": f"run:{tools.command.run_id}"}}
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                with pytest.raises(RuntimeError):
                    await graph.ainvoke({"content": "创建工单 FD202611020002"}, config)
                checkpoint = await graph.aget_state(config)
                action_id = checkpoint.values["action_id"]
                assert checkpoint.next == ("prepare_action",)
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke(None, config)
                checkpoint = await graph.aget_state(config)
                assert any(task.interrupts for task in checkpoint.tasks)
                assert tools.allocated == [action_id, action_id]
                assert not tools.effects
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                with pytest.raises(RuntimeError):
                    await graph.ainvoke(
                        Command(resume={"decision": "approved", "action_id": action_id}), config
                    )
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn), FailedSummary())
                with pytest.raises(ToolError, match="MODEL_OUTPUT_INVALID"):
                    await graph.ainvoke(None, config)
                checkpoint = await graph.aget_state(config)
                assert checkpoint.values["receipt"]["action_id"] == action_id
                assert checkpoint.next == ("finalize",)
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke(None, config)
            assert len(tools.effects) == 1
            assert tools.events[-1][0] == "run.succeeded"
            assert tools.events[-2][1]["action_receipts"] == [{"action_id": action_id}]

    asyncio.run(scenario())


def test_missing_order_interrupt_survives_connection_replacement():
    async def scenario():
        async with isolated_pool() as pool:
            tools = DurableTools(command())
            tools.prepare_crash = False
            config = {"configurable": {"thread_id": f"run:{tools.command.run_id}"}}
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke({"content": "请创建工单"}, config)
                state = await graph.aget_state(config)
                input_id = state.values["input_id"]
                assert not tools.allocated and not tools.effects
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke(
                    Command(resume={"input_id": input_id, "input": "FD202611020002"}), config
                )
                state = await graph.aget_state(config)
                assert state.values["plan"]["order_no"] == "FD202611020002"
                assert any(task.interrupts for task in state.tasks)
                assert not tools.effects

    asyncio.run(scenario())


def test_generation_fence_and_durable_cancellation_block_old_worker():
    async def scenario():
        async with isolated_pool() as pool, pool.connection() as conn:
            commands = CommandStore(pool)
            item = command()
            await commands.accept(item)
            first = await commands.claim()
            assert await commands.own(conn, item, first["lease_owner"])
            assert await commands.still_owned(conn, item.dispatch_id, first["lease_owner"])
            resumed = item.model_copy(
                update={"dispatch_id": uuid4(), "lease_generation": 2, "command": "resume"}
            )
            await commands.accept(resumed)
            second = await commands.claim()
            assert await commands.own(conn, resumed, second["lease_owner"])
            assert not await commands.own(conn, item, first["lease_owner"])
            assert not await commands.still_owned(conn, item.dispatch_id, first["lease_owner"])
            cancellation = CancelCommand(
                dispatch_id=uuid4(), run_id=item.run_id, lease_generation=3
            )
            await commands.cancel(cancellation)
            await commands.cancel(cancellation)
            assert not await commands.still_owned(conn, resumed.dispatch_id, second["lease_owner"])
            await commands.release(resumed.dispatch_id, second["lease_owner"], "queued")
            assert (await commands.find(resumed.dispatch_id))["status"] == "cancelled"
            assert (await commands.accept(item))["status"] == "cancelled"
            assert await commands.claim() is None
            # A lost checkpoint session cannot be used by the old executor.
            lock_key = str(item.run_id)
            await conn.execute("SELECT pg_advisory_lock(hashtextextended(%s,3))", (lock_key,))
            async with pool.connection() as replacement:
                row = await (
                    await replacement.execute(
                        "SELECT pg_try_advisory_lock(hashtextextended(%s,3)) AS acquired",
                        (lock_key,),
                    )
                ).fetchone()
                assert not row["acquired"]
            await conn.close()
            with pytest.raises(OperationalError):
                await commands.still_owned(conn, resumed.dispatch_id, second["lease_owner"])
            async with pool.connection() as replacement:
                row = await (
                    await replacement.execute(
                        "SELECT pg_try_advisory_lock(hashtextextended(%s,3)) AS acquired",
                        (lock_key,),
                    )
                ).fetchone()
                assert row["acquired"]
                await replacement.execute(
                    "SELECT pg_advisory_unlock(hashtextextended(%s,3))", (lock_key,)
                )

    asyncio.run(scenario())
