import asyncio

from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.runtime import execution_guard
from flowdesk_agent.storage.commands import CommandStore


def test_ownership_query_waits_for_checkpoint_pipeline():
    async def scenario():
        async with isolated_pool() as pool, pool.connection() as conn:
            commands = CommandStore(pool)
            item = command()
            await commands.accept(item)
            row = await commands.claim()
            saver = AsyncPostgresSaver(conn)
            guard = execution_guard(commands, conn, saver, item, row["lease_owner"])
            entered = asyncio.Event()
            release = asyncio.Event()

            async def checkpoint():
                async with saver._cursor(pipeline=True) as cursor:
                    await cursor.execute("SELECT 1")
                    entered.set()
                    await release.wait()

            writer = asyncio.create_task(checkpoint())
            await entered.wait()
            probe = asyncio.create_task(guard())
            await asyncio.sleep(0.03)
            assert not probe.done()
            release.set()
            await asyncio.wait_for(asyncio.gather(writer, probe), timeout=5)

    asyncio.run(scenario())
