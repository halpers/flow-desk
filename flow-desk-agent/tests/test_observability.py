import asyncio

import pytest
from langgraph.errors import GraphInterrupt
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.observability.spans import SpanRecorder


def test_trace_records_failures_and_interrupts_without_state_or_reasoning():
    async def scenario():
        async with isolated_pool() as pool:
            item = command()
            recorder = SpanRecorder(pool, item)

            async def succeed(state):
                return {"answer": state["private"]}

            async def pause(state):
                raise GraphInterrupt()

            async def fail(state):
                raise ValueError("private failure text")

            secret_state = {"private": "DO_NOT_STORE_PRIVATE_STATE"}
            assert await recorder.wrap("finalize", succeed)(secret_state) == {
                "answer": secret_state["private"]
            }
            with pytest.raises(GraphInterrupt):
                await recorder.wrap("wait_input", pause)(secret_state)
            with pytest.raises(ValueError):
                await recorder.wrap("execute_action", fail)(secret_state)
            async with pool.connection() as conn:
                rows = await (
                    await conn.execute(
                        "SELECT to_jsonb(s) AS data FROM execution_spans s "
                        "WHERE run_id=%s ORDER BY completed_at",
                        (item.run_id,),
                    )
                ).fetchall()
            assert [row["data"]["status"] for row in rows] == ["completed", "interrupted", "failed"]
            assert all(row["data"]["duration_ms"] >= 0 for row in rows)
            assert "DO_NOT_STORE_PRIVATE_STATE" not in str(rows)
            assert "private failure text" not in str(rows)

    asyncio.run(scenario())
