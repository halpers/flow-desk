from uuid import UUID

from psycopg.types.json import Jsonb
from psycopg_pool import AsyncConnectionPool


class ToolCache:
    def __init__(self, pool: AsyncConnectionPool):
        self.pool = pool

    async def find(self, call_id: UUID):
        async with self.pool.connection() as conn:
            row = await (
                await conn.execute("SELECT result FROM tool_results WHERE id=%s", (call_id,))
            ).fetchone()
            return row["result"] if row else None

    async def insert(
        self, call_id: UUID, dispatch_id: UUID, tool: str, arguments: dict, result: dict
    ):
        async with self.pool.connection() as conn:
            await conn.execute(
                "INSERT INTO "
                "tool_results(id,dispatch_id,tool_name,arguments,result) VALUES "
                "(%s,%s,%s,%s,%s) ON CONFLICT DO NOTHING",
                (call_id, dispatch_id, tool, Jsonb(arguments), Jsonb(result)),
            )
