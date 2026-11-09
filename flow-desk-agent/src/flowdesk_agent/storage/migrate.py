"""One-shot deployment migration. Never imported or run by the HTTP lifespan."""

import asyncio
import os

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.storage.migrations import migrate
from flowdesk_agent.storage.permissions import MIGRATOR_ROLE, grant_runtime


async def run():
    dsn = os.environ.get("AGENT_MIGRATION_DATABASE_URL")
    if not dsn:
        raise RuntimeError("AGENT_MIGRATION_DATABASE_NOT_CONFIGURED")
    async with AsyncConnectionPool(
        dsn,
        min_size=1,
        max_size=1,
        open=False,
        kwargs={"autocommit": True, "row_factory": dict_row, "prepare_threshold": 0},
    ) as pool:
        await pool.wait(timeout=10)
        async with pool.connection() as connection:
            row = await (
                await connection.execute("SELECT current_user AS name,current_schema() AS schema")
            ).fetchone()
            if row != {"name": MIGRATOR_ROLE, "schema": "agent"}:
                raise RuntimeError("AGENT_MIGRATION_IDENTITY_INVALID")
        await migrate(pool)
        async with pool.connection() as connection, connection.transaction():
            await grant_runtime(connection)
    print("Agent schema and checkpoint migrations complete; runtime DML granted.")


def main():
    try:
        asyncio.run(run())
    except (RuntimeError, psycopg.Error, OSError):
        raise SystemExit(
            "AGENT_MIGRATION_FAILED; inspect role, schema version and migration job"
        ) from None


if __name__ == "__main__":
    main()
