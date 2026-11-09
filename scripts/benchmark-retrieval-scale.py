"""Synthetic exact/tenant-profile HNSW comparison; disposable schema, no model calls."""

import argparse
import asyncio
import json
import math
import os
import random
import statistics
import sys
import time
from contextlib import asynccontextmanager
from pathlib import Path
from uuid import UUID, uuid4

import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "flow-desk-agent/src"))
from flowdesk_agent.ann_index import (
    SearchSettings,
    SemanticSearch,
    ann_query,
    maintain_index,
    search_settings,
)
from flowdesk_agent.embeddings import vector_literal
from flowdesk_agent.storage.migrations import migrate

PROFILE = "semantic-v1-" + "c" * 24


def percentile(values, fraction):
    return round(
        sorted(values)[min(len(values) - 1, math.ceil(len(values) * fraction) - 1)], 3
    )


@asynccontextmanager
async def temporary_pool(dsn):
    schema = "fd_retrieval_scale_" + uuid4().hex
    async with await psycopg.AsyncConnection.connect(
        dsn, autocommit=True, row_factory=dict_row
    ) as owner:
        await owner.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
        try:
            async with AsyncConnectionPool(
                make_conninfo(dsn, options="-csearch_path=" + schema),
                min_size=1,
                max_size=4,
                open=False,
                kwargs={
                    "autocommit": True,
                    "row_factory": dict_row,
                    "prepare_threshold": 0,
                },
            ) as pool:
                await pool.wait(timeout=10)
                await migrate(pool)
                yield pool
        finally:
            await owner.execute(
                sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema))
            )


async def populate(pool, args):
    rng = random.Random(722)
    tenant, foreign = UUID(int=722), UUID(int=723)
    versions = [UUID(int=1000 + index) for index in range(4)]
    dimension = args.dimension
    vectors = []
    async with pool.connection() as connection, connection.transaction():
        for index, version in enumerate(versions):
            owner = foreign if index == 3 else tenant
            await connection.execute(
                "INSERT INTO knowledge_manifests(version_id,tenant_id,knowledge_base_id,document_id,"
                "content_hash,profile,dimension,splitter_version,chunk_count) "
                "VALUES(%s,%s,%s,%s,'synthetic','char-ngram-v1',256,'synthetic',%s)",
                (
                    version,
                    owner,
                    UUID(int=10),
                    UUID(int=100 + index),
                    (args.rows + 3 - index) // 4,
                ),
            )
            await connection.execute(
                "INSERT INTO semantic_manifests(version_id,profile,tenant_id,content_hash,model,dimension,"
                "chunk_count) VALUES(%s,%s,%s,'synthetic','synthetic',%s,%s)",
                (version, PROFILE, owner, dimension, (args.rows + 3 - index) // 4),
            )
        async with connection.cursor() as cursor:
            async with cursor.copy(
                "COPY knowledge_chunks(chunk_id,version_id,tenant_id,position,content,content_hash,lexical,embedding) FROM STDIN"
            ) as copier:
                for index in range(args.rows):
                    group = index % 4
                    values = [rng.gauss(0, 1) for _ in range(dimension)]
                    norm = math.sqrt(sum(value * value for value in values))
                    values = [value / norm for value in values]
                    vectors.append(values)
                    await copier.write_row(
                        (
                            UUID(int=10000 + index),
                            versions[group],
                            foreign if group == 3 else tenant,
                            index // 4,
                            "synthetic chunk",
                            "synthetic",
                            "",
                            vector_literal([1.0] + [0.0] * 255),
                        )
                    )
            async with cursor.copy(
                "COPY semantic_vectors(chunk_id,version_id,tenant_id,profile,dimension,embedding) FROM STDIN"
            ) as copier:
                for index, values in enumerate(vectors):
                    group = index % 4
                    await copier.write_row(
                        (
                            UUID(int=10000 + index),
                            versions[group],
                            foreign if group == 3 else tenant,
                            PROFILE,
                            dimension,
                            vector_literal(values),
                        )
                    )
        await connection.execute("ANALYZE semantic_vectors")
        await connection.execute("ANALYZE knowledge_chunks")
    return tenant, versions, vectors


async def run(args):
    dsn = os.environ.get("AGENT_TEST_DATABASE_URL")
    if not dsn:
        raise ValueError(
            "AGENT_TEST_DATABASE_URL is required; use a disposable-schema administrator"
        )
    async with temporary_pool(dsn) as pool:
        tenant, versions, vectors = await populate(pool, args)
        async with await psycopg.AsyncConnection.connect(
            pool.conninfo, autocommit=True, row_factory=dict_row
        ) as connection:
            started = time.perf_counter()
            registry = await maintain_index(
                connection, "create", tenant, PROFILE, args.dimension, confirm=True
            )
            build_ms = (time.perf_counter() - started) * 1000
            size = await (
                await connection.execute(
                    "SELECT pg_relation_size(%s::regclass) AS bytes",
                    (registry["index_name"],),
                )
            ).fetchone()
            version = await (
                await connection.execute(
                    "SELECT current_setting('server_version') AS postgres,"
                    "(SELECT extversion FROM pg_extension WHERE extname='vector') AS pgvector"
                )
            ).fetchone()
        exact = SemanticSearch(pool, SearchSettings())
        ann = SemanticSearch(pool, SearchSettings("hnsw", args.ef_search, 20000))
        timings = {"exact": [], "hnsw": []}
        recalls, modes, cases = [], {}, []
        for index in range(args.queries):
            values = vectors[(index * 97) % len(vectors)]
            # Two of three same-tenant versions are currently authorized. Foreign
            # IDs deliberately appear in the supplied list and must still be excluded.
            allowed = [versions[0], versions[1], versions[3]]
            results = {}
            for label, search in (("exact", exact), ("hnsw", ann)):
                started = time.perf_counter()
                result = await search.candidates(tenant, PROFILE, allowed, values)
                timings[label].append((time.perf_counter() - started) * 1000)
                assert len(result.rows) == 20
                assert all(
                    row["tenant_id"] == tenant and row["version_id"] in versions[:2]
                    for row in result.rows
                )
                results[label] = {str(row["chunk_id"]) for row in result.rows}
                if label == "hnsw":
                    modes[result.mode + ":" + result.reason] = (
                        modes.get(result.mode + ":" + result.reason, 0) + 1
                    )
            recall = len(results["exact"] & results["hnsw"]) / 20
            recalls.append(recall)
            cases.append({"query": index, "recall_at_20": recall})
        literal = vector_literal(vectors[0])
        async with pool.connection() as connection, connection.transaction():
            await search_settings(connection, ann.settings)
            plan = await (
                await connection.execute(
                    sql.SQL("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) ")
                    + ann_query(tenant, PROFILE, args.dimension),
                    (literal, [versions[0], versions[1], versions[3]], literal),
                    prepare=False,
                )
            ).fetchone()
            actual_plan = next(iter(plan.values()))
        assert registry["index_name"] in json.dumps(actual_plan), (
            "Natural query planner did not choose the HNSW index"
        )
        report = {
            "synthetic": True,
            "isolated_schema": True,
            "model_calls": 0,
            "seed": 722,
            "rows": args.rows,
            "dimension": args.dimension,
            "queries": args.queries,
            "filter": "tenant/profile/dimension partial index; two authorized versions; foreign and forbidden distractors",
            "configuration": {
                "ef_search": args.ef_search,
                "max_scan_tuples": 20000,
                "candidate_limit": 20,
            },
            "versions": version,
            "index_build_ms": round(build_ms, 3),
            "index_bytes": size["bytes"],
            "mean_recall_at_20": round(statistics.mean(recalls), 5),
            "minimum_recall_at_20": min(recalls),
            "actual_modes": modes,
            "cases": cases,
            "latency_ms": {
                name: {"p50": percentile(values, 0.5), "p95": percentile(values, 0.95)}
                for name, values in timings.items()
            },
            "plan": actual_plan,
            "limitations": "Synthetic warm queries include registry/catalog checks; not embedding/model latency, semantic quality, production throughput or a recall guarantee.",
        }
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        print(
            json.dumps(
                {
                    key: value
                    for key, value in report.items()
                    if key not in ("cases", "plan")
                },
                ensure_ascii=False,
            )
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rows", type=int, default=10000)
    parser.add_argument("--dimension", type=int, default=64)
    parser.add_argument("--queries", type=int, default=40)
    parser.add_argument("--ef-search", type=int, default=100)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if (
        not 1000 <= args.rows <= 100000
        or not 3 <= args.dimension <= 2000
        or not 10 <= args.queries <= 200
    ):
        parser.error("Use rows=1000–100000, dimensions=3–2000 and queries=10–200")
    SearchSettings("hnsw", args.ef_search)
    asyncio.run(run(args))


if __name__ == "__main__":
    main()
