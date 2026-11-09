import asyncio
import json
from uuid import uuid4

import psycopg
import pytest
from psycopg import sql
from psycopg.rows import dict_row
from test_durable_runtime import isolated_pool

from flowdesk_agent.ann_index import (
    SearchSettings,
    SemanticSearch,
    ann_query,
    identity,
    maintain_index,
    search_settings,
)
from flowdesk_agent.embeddings import vector_literal
from flowdesk_agent.tools.go_client import ToolError

PROFILE = "semantic-v1-" + "a" * 24
OTHER_PROFILE = "semantic-v1-" + "b" * 24
ann_pool = isolated_pool


async def fixture(pool, *, count=200, dimension=3, tenant=None, profile=PROFILE, version=None):
    tenant, version = tenant or uuid4(), version or uuid4()
    async with pool.connection() as connection, connection.transaction():
        await connection.execute(
            "INSERT INTO knowledge_manifests(version_id,tenant_id,knowledge_base_id,document_id,"
            "content_hash,profile,dimension,splitter_version,chunk_count) "
            "VALUES(%s,%s,%s,%s,'synthetic','char-ngram-v1',256,'synthetic',%s)",
            (version, tenant, uuid4(), uuid4(), count),
        )
        await connection.execute(
            "INSERT INTO semantic_manifests(version_id,profile,tenant_id,content_hash,"
            "model,dimension,"
            "chunk_count) VALUES(%s,%s,%s,'synthetic','synthetic',%s,%s)",
            (version, profile, tenant, dimension, count),
        )
        for position in range(count):
            chunk_id = uuid4()
            values = [1.0, (position + 1) / count] + [0.0] * (dimension - 2)
            await connection.execute(
                "INSERT INTO knowledge_chunks(chunk_id,version_id,tenant_id,position,content,"
                "content_hash,lexical,embedding) VALUES(%s,%s,%s,%s,'synthetic','synthetic',"
                "to_tsvector('simple',''),%s::knowledge.vector)",
                (chunk_id, version, tenant, position, vector_literal([1.0] + [0.0] * 255)),
            )
            await connection.execute(
                "INSERT INTO semantic_vectors(chunk_id,version_id,tenant_id,"
                "profile,dimension,embedding) "
                "VALUES(%s,%s,%s,%s,%s,%s::knowledge.vector)",
                (chunk_id, version, tenant, profile, dimension, vector_literal(values)),
            )
    return tenant, version


async def maintain(pool, action, tenant, *, profile=PROFILE, dimension=3, confirm=True):
    async with await psycopg.AsyncConnection.connect(
        pool.conninfo, autocommit=True, row_factory=dict_row
    ) as connection:
        return await maintain_index(connection, action, tenant, profile, dimension, confirm=confirm)


def test_settings_and_identity_reject_unbounded_or_untrusted_inputs():
    assert SearchSettings.from_environment({}) == SearchSettings()
    for config in (
        {"SEMANTIC_SEARCH_MODE": "automatic"},
        {"SEMANTIC_HNSW_EF_SEARCH": "1001"},
        {"SEMANTIC_HNSW_EF_SEARCH": "39"},
        {"SEMANTIC_HNSW_EF_SEARCH": "NaN"},
        {"SEMANTIC_HNSW_MAX_SCAN_TUPLES": "999"},
        {"SEMANTIC_HNSW_MAX_SCAN_TUPLES": "100001"},
    ):
        with pytest.raises(ToolError, match="SEMANTIC_SEARCH_CONFIG_INVALID"):
            SearchSettings.from_environment(config)
    for tenant, profile, dimension in (
        ("invalid", PROFILE, 3),
        (uuid4(), "evil'); DROP TABLE semantic_vectors;--", 3),
        (uuid4(), PROFILE, 2001),
        (uuid4(), PROFILE, 0),
        (uuid4(), PROFILE, True),
    ):
        with pytest.raises(ToolError, match="SEMANTIC_INDEX_ARGUMENT_INVALID"):
            identity(tenant, profile, dimension)


def test_index_maintenance_is_idempotent_recoverable_and_lock_scoped():
    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=40)
            with pytest.raises(ToolError, match="CONFIRM_REQUIRED"):
                await maintain(pool, "create", tenant, confirm=False)
            result = await maintain(pool, "create", tenant)
            assert result["state"] == "ready"
            assert await maintain(pool, "create", tenant) == result
            assert await maintain(pool, "status", tenant) == result
            async with pool.connection() as connection:
                await connection.execute(
                    "UPDATE semantic_ann_indexes SET state='building',definition_hash=''"
                )
            # Simulate a crash after successful CREATE but before registry publication.
            assert await maintain(pool, "create", tenant) == result
            async with pool.connection() as connection:
                lock_key = f"semantic-ann:{tenant}:{PROFILE}:3"
                await connection.execute(
                    "SELECT pg_advisory_lock(hashtextextended(%s,0))", (lock_key,)
                )
                try:
                    with pytest.raises(ToolError, match="MAINTENANCE_BUSY"):
                        await maintain(pool, "drop", tenant)
                finally:
                    await connection.execute(
                        "SELECT pg_advisory_unlock(hashtextextended(%s,0))", (lock_key,)
                    )
            assert (await maintain(pool, "drop", tenant))["state"] == "absent"
            assert (await maintain(pool, "status", tenant))["state"] == "unavailable"
            assert (await maintain(pool, "drop", tenant))["state"] == "absent"
            # No knowledge rows are deleted by index retirement.
            async with pool.connection() as connection:
                row = await (
                    await connection.execute(
                        "SELECT count(*) AS total FROM semantic_vectors WHERE version_id=%s",
                        (version,),
                    )
                ).fetchone()
                assert row["total"] == 40

    asyncio.run(scenario())


def test_ann_filters_before_limit_and_exact_default_preserves_order():
    async def scenario():
        async with ann_pool() as pool:
            tenant, allowed = await fixture(pool, count=300)
            _, hidden = await fixture(pool, count=300, tenant=tenant)
            foreign, foreign_version = await fixture(pool, count=100)
            # Dense forbidden candidates are strictly closer to query than authorized rows.
            async with pool.connection() as connection:
                await connection.execute(
                    "UPDATE semantic_vectors SET embedding='[1,0,0]'::knowledge.vector "
                    "WHERE version_id=ANY(%s)",
                    ([hidden, foreign_version],),
                )
            await fixture(pool, count=100, tenant=tenant, profile=OTHER_PROFILE)
            result = await maintain(pool, "create", tenant)
            assert result["state"] == "ready"
            exact = SemanticSearch(pool, SearchSettings())
            ann = SemanticSearch(pool, SearchSettings("hnsw", 100, 20000))
            rows = await exact.candidates(tenant, PROFILE, [allowed, foreign_version], [1, 0, 0])
            assert rows.mode == "exact" and len(rows.rows) == 20
            approximate = await ann.candidates(
                tenant, PROFILE, [allowed, foreign_version], [1, 0, 0]
            )
            assert len(approximate.rows) == 20
            assert all(
                item["version_id"] == allowed and item["tenant_id"] == tenant
                for item in approximate.rows
            )
            assert len({item["chunk_id"] for item in approximate.rows}) == 20
            assert all(item["rank"] < 1 for item in approximate.rows)
            # Even an index-favoring planner must not replace the exact query
            # with approximate nearest-neighbor ordering.
            async with pool.connection() as connection, connection.transaction():
                await connection.execute("SET LOCAL enable_seqscan=off")
                plan = await (
                    await connection.execute(
                        "EXPLAIN (FORMAT JSON) " + exact.query,
                        ("[1,0,0]", tenant, PROFILE, [allowed], "[1,0,0]"),
                    )
                ).fetchone()
                assert result["index_name"] not in json.dumps(plan)
            # Local settings have not leaked from the ANN transaction into pooled connections.
            async with pool.connection() as connection:
                row = await (await connection.execute("SHOW plan_cache_mode")).fetchone()
                assert row["plan_cache_mode"] == "auto"
            assert foreign != tenant

    asyncio.run(scenario())


def test_missing_invalid_and_dimension_indexes_fall_back_to_exact():
    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=30)
            search = SemanticSearch(pool, SearchSettings("hnsw"))
            assert (
                await search.candidates(tenant, PROFILE, [version], [1, 0, 0])
            ).reason == "index_unavailable"
            await maintain(pool, "create", tenant)
            async with pool.connection() as connection:
                await connection.execute(
                    "UPDATE semantic_ann_indexes SET definition_hash=%s", ("0" * 64,)
                )
            assert (
                await search.candidates(tenant, PROFILE, [version], [1, 0, 0])
            ).reason == "index_unavailable"
            # Correctly recover the known index and exercise small permitted scopes.
            await maintain(pool, "create", tenant)
            _, small = await fixture(pool, count=3, tenant=tenant)
            result = await search.candidates(tenant, PROFILE, [small], [1, 0, 0])
            assert result.mode == "exact" and result.reason == "insufficient_candidates"
            assert len(result.rows) == 3
            large_tenant, large = await fixture(pool, count=2, dimension=2001)
            result = await search.candidates(large_tenant, PROFILE, [large], [1, 0] + [0] * 1999)
            assert result.mode == "exact" and result.reason == "unsupported_dimension"
            assert len(result.rows) == 2

    asyncio.run(scenario())


def test_wrong_index_definition_is_never_adopted_or_dropped():
    async def scenario():
        async with ann_pool() as pool:
            tenant, _ = await fixture(pool, count=30)
            _, name = identity(tenant, PROFILE, 3)
            async with pool.connection() as connection:
                await connection.execute(
                    sql.SQL("CREATE INDEX {} ON semantic_vectors(tenant_id)").format(
                        sql.Identifier(name)
                    )
                )
            for action in ("create", "drop", "status"):
                with pytest.raises(ToolError, match="DEFINITION_MISMATCH"):
                    await maintain(pool, action, tenant)

    asyncio.run(scenario())


def test_real_plan_uses_tenant_profile_hnsw_and_exact_has_no_ann():
    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=600)
            result = await maintain(pool, "create", tenant)
            async with pool.connection() as connection, connection.transaction():
                await connection.execute("ANALYZE semantic_vectors")
                await connection.execute("ANALYZE knowledge_chunks")
                await search_settings(connection, SearchSettings("hnsw"))
                # Tiny fixtures can favor exact sorting. Disable sequential scans
                # here to demonstrate actual access-method use.
                await connection.execute("SET LOCAL enable_seqscan=off")
                await connection.execute("SET LOCAL enable_sort=off")
                plan = await (
                    await connection.execute(
                        sql.SQL("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) ")
                        + ann_query(tenant, PROFILE, 3),
                        ("[1,0,0]", [version], "[1,0,0]"),
                        prepare=False,
                    )
                ).fetchone()
                assert result["index_name"] in json.dumps(plan)

    asyncio.run(scenario())


def test_registry_building_and_concurrent_drop_use_exact():
    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=40)
            result = await maintain(pool, "create", tenant)
            search = SemanticSearch(pool, SearchSettings("hnsw"))
            async with pool.connection() as connection:
                await connection.execute("UPDATE semantic_ann_indexes SET state='building'")
            assert (await search.candidates(tenant, PROFILE, [version], [1, 0, 0])).mode == "exact"
            await maintain(pool, "create", tenant)
            # A physical index can disappear after an operator inspects registry state.
            # Missing catalog entries are unavailable, never proof of an empty knowledge set.
            async with pool.connection() as connection:
                await connection.execute(
                    sql.SQL("DROP INDEX {}").format(sql.Identifier(result["index_name"]))
                )
            result = await search.candidates(tenant, PROFILE, [version], [1, 0, 0])
            assert result.mode == "exact" and result.reason == "index_unavailable"
            assert len(result.rows) == 20
            assert (await maintain(pool, "create", tenant))["state"] == "ready"

    asyncio.run(scenario())


def test_exact_query_timeout_is_bounded_and_connection_settings_are_local(monkeypatch):
    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=30)
            monkeypatch.setattr("flowdesk_agent.ann_index.QUERY_TIMEOUT_MS", 100)
            async with pool.connection() as blocker, blocker.transaction():
                await blocker.execute("LOCK TABLE semantic_vectors IN ACCESS EXCLUSIVE MODE")
                with pytest.raises(psycopg.errors.QueryCanceled):
                    await SemanticSearch(pool).candidates(tenant, PROFILE, [version], [1, 0, 0])
            assert (
                len(
                    (
                        await SemanticSearch(pool).candidates(tenant, PROFILE, [version], [1, 0, 0])
                    ).rows
                )
                == 20
            )
            async with pool.connection() as connection:
                row = await (await connection.execute("SHOW statement_timeout")).fetchone()
                assert row["statement_timeout"] == "0"

    asyncio.run(scenario())


def test_retriever_uses_same_semantic_candidates_and_keeps_fusion_contract():
    from types import SimpleNamespace

    from flowdesk_agent.retrieval import Retriever

    async def scenario():
        async with ann_pool() as pool:
            tenant, version = await fixture(pool, count=50)
            await maintain(pool, "create", tenant)

            async def encode(*args, **kwargs):
                return [[1, 0, 0]]

            embedder = SimpleNamespace(profile=PROFILE, encode=encode)
            ranker = Retriever(
                pool,
                None,
                embedder=embedder,
                profile="semantic",
                search_settings=SearchSettings("hnsw"),
            )
            result = await ranker.rank("synthetic", tenant, [version], 5)
            assert len(result) == 5
            assert all(
                row["tenant_id"] == tenant and row["version_id"] == version for row in result
            )

    asyncio.run(scenario())
