"""Operator-only semantic rebuild, restricted to an explicitly selected tenant."""

import argparse
import asyncio
import json
import os
from uuid import UUID

from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.embeddings import configured_embedder
from flowdesk_agent.semantic_index import SemanticIndex
from flowdesk_agent.tools.go_client import ToolError


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tenant-id", type=UUID, required=True)
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument("--version-id", type=UUID, action="append")
    selection.add_argument("--all", action="store_true")
    args = parser.parse_args()
    async with AsyncConnectionPool(
        os.environ["AGENT_DATABASE_URL"],
        min_size=1,
        max_size=4,
        open=False,
        kwargs={"autocommit": True, "row_factory": dict_row, "prepare_threshold": 0},
    ) as pool:
        await pool.wait(timeout=10)
        embedder = configured_embedder(pool)
        if embedder is None:
            raise ToolError("EMBEDDING_NOT_CONFIGURED")
        async with pool.connection() as connection:
            versions = await (
                await connection.execute(
                    "SELECT version_id FROM knowledge_manifests WHERE tenant_id=%s "
                    "AND (%s OR version_id=ANY(%s)) ORDER BY version_id",
                    (args.tenant_id, args.all, args.version_id or []),
                )
            ).fetchall()
        if args.version_id and {row["version_id"] for row in versions} != set(args.version_id):
            raise ToolError("INDEX_SCOPE_REJECTED")
        rebuilt = 0
        for row in versions:
            rebuilt += await SemanticIndex(pool, embedder).rebuild(row["version_id"])
        print(
            json.dumps(
                {
                    "profile": embedder.profile,
                    "versions": len(versions),
                    "rebuilt": rebuilt,
                    "dimension": embedder.dimension,
                    "activation": "unchanged",
                }
            )
        )


if __name__ == "__main__":
    asyncio.run(main())
