"""Actual login roles and checkpointer writes in a unique disposable database."""

import asyncio
import os
import secrets
from contextlib import asynccontextmanager
from typing import TypedDict
from uuid import uuid4

import psycopg
import pytest
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.graph import END, START, StateGraph
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool
from test_ann_retrieval import fixture as semantic_fixture

from flowdesk_agent.ann_index import SearchSettings, SemanticSearch, maintain_index
from flowdesk_agent.storage.migrations import MIGRATIONS, migrate
from flowdesk_agent.storage.permissions import grant_runtime, verify_runtime
from flowdesk_agent.storage.upgrade_roles import upgrade


@asynccontextmanager
async def roles():
    dsn = os.getenv("AGENT_TEST_DATABASE_URL")
    if not dsn:
        pytest.skip("AGENT_TEST_DATABASE_URL requires disposable-database administrator")
    suffix = uuid4().hex
    database, migrator, runtime = ("fd_" + prefix + suffix for prefix in ("db_", "m_", "r_"))
    password = secrets.token_hex(24)
    async with await psycopg.AsyncConnection.connect(
        dsn, autocommit=True, row_factory=dict_row
    ) as cluster:
        for role in (migrator, runtime):
            await cluster.execute(
                sql.SQL("CREATE ROLE {} LOGIN PASSWORD {}").format(
                    sql.Identifier(role), sql.Literal(password)
                )
            )
        await cluster.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(database)))
        try:
            owner_dsn = make_conninfo(dsn, dbname=database)
            async with await psycopg.AsyncConnection.connect(
                owner_dsn, autocommit=True, row_factory=dict_row
            ) as owner:
                await owner.execute(
                    sql.SQL("REVOKE CREATE,TEMP ON DATABASE {} FROM PUBLIC").format(
                        sql.Identifier(database)
                    )
                )
                await owner.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
                for schema in ("agent", "knowledge"):
                    await owner.execute(
                        sql.SQL("CREATE SCHEMA {} AUTHORIZATION {}").format(
                            sql.Identifier(schema), sql.Identifier(migrator)
                        )
                    )
                await owner.execute("CREATE EXTENSION vector WITH SCHEMA knowledge")
                await owner.execute("CREATE SCHEMA business")
                await owner.execute("CREATE TABLE business.sentinel(id integer PRIMARY KEY)")
                await owner.execute("INSERT INTO business.sentinel VALUES(1)")
                connections = dict(
                    open=False,
                    min_size=1,
                    max_size=3,
                    kwargs={"autocommit": True, "row_factory": dict_row, "prepare_threshold": 0},
                )
                async with (
                    AsyncConnectionPool(
                        make_conninfo(
                            owner_dsn,
                            user=migrator,
                            password=password,
                            options="-csearch_path=agent",
                        ),
                        **connections,
                    ) as migration_pool,
                    AsyncConnectionPool(
                        make_conninfo(
                            owner_dsn,
                            user=runtime,
                            password=password,
                            options="-csearch_path=agent",
                        ),
                        **connections,
                    ) as runtime_pool,
                ):
                    await migration_pool.wait(timeout=10)
                    await runtime_pool.wait(timeout=10)
                    await migrate(migration_pool)
                    async with migration_pool.connection() as connection:
                        await grant_runtime(connection, role=runtime)
                    yield runtime_pool, migration_pool, owner, runtime, migrator
        finally:
            await cluster.execute(
                sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database))
            )
            for role in (runtime, migrator):
                await cluster.execute(sql.SQL("DROP ROLE {}").format(sql.Identifier(role)))


def test_runtime_login_can_checkpoint_and_queue_but_cannot_ddl_or_write_business():
    class State(TypedDict):
        value: int

    async def scenario():
        async with roles() as (pool, migration_pool, _, runtime, migrator):
            await verify_runtime(pool, role=runtime)
            saver = AsyncPostgresSaver(pool)
            graph = StateGraph(State)
            graph.add_node("increment", lambda state: {"value": state["value"] + 1})
            graph.add_edge(START, "increment")
            graph.add_edge("increment", END)
            app = graph.compile(checkpointer=saver)
            config = {"configurable": {"thread_id": "run:" + str(uuid4())}}
            assert await app.ainvoke({"value": 1}, config) == {"value": 2}
            recovered = graph.compile(checkpointer=AsyncPostgresSaver(pool))
            assert (await recovered.aget_state(config)).values == {"value": 2}
            async with pool.connection() as connection:
                await connection.execute(
                    "INSERT INTO execution_commands(dispatch_id,run_id,lease_generation,"
                    "command,request_hash,body) VALUES(%s,%s,1,'execute','synthetic','{}')",
                    (uuid4(), uuid4()),
                )
                for query in (
                    "CREATE TABLE agent.denied(id integer)",
                    "CREATE TABLE public.denied(id integer)",
                    "CREATE TEMP TABLE denied(id integer)",
                    "CREATE SCHEMA denied",
                    "ALTER TABLE execution_commands ADD denied integer",
                    "DROP TABLE execution_commands",
                    "TRUNCATE execution_commands",
                    "UPDATE schema_versions SET applied_at=now()",
                    "DELETE FROM checkpoint_migrations",
                    "SELECT * FROM business.sentinel",
                    sql.SQL("SET ROLE {}").format(sql.Identifier(migrator)),
                ):
                    with pytest.raises(psycopg.errors.InsufficientPrivilege):
                        await connection.execute(query)
            await migrate(migration_pool)
            async with migration_pool.connection() as connection:
                await grant_runtime(connection, role=runtime)
            await verify_runtime(pool, role=runtime)

    asyncio.run(scenario())


def test_runtime_refuses_missing_future_and_changed_checkpoint_migrations():
    async def scenario():
        async with roles() as (pool, migration_pool, _, runtime, _):
            async with migration_pool.connection() as connection:
                await connection.execute(
                    "INSERT INTO schema_versions(version) VALUES(%s)", (len(MIGRATIONS) + 1,)
                )
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await verify_runtime(pool, role=runtime)
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await migrate(migration_pool)
            async with migration_pool.connection() as connection:
                await connection.execute(
                    "DELETE FROM schema_versions WHERE version=%s", (len(MIGRATIONS) + 1,)
                )
                await connection.execute("DELETE FROM schema_versions WHERE version=1")
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await verify_runtime(pool, role=runtime)
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await migrate(migration_pool)
            async with migration_pool.connection() as connection:
                await connection.execute("INSERT INTO schema_versions(version) VALUES(1)")
                await connection.execute("INSERT INTO checkpoint_migrations(v) VALUES(9999)")
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await verify_runtime(pool, role=runtime)
            with pytest.raises(RuntimeError, match="VERSION_MISMATCH"):
                await migrate(migration_pool)

    asyncio.run(scenario())


def test_ownership_upgrade_rejects_unsafe_migrator_and_rolls_back_late_failure():
    async def scenario():
        async with roles() as (pool, migration_pool, owner, runtime, migrator):
            await pool.close()
            await owner.execute(sql.SQL("ALTER ROLE {} CREATEDB").format(sql.Identifier(migrator)))
            with pytest.raises(RuntimeError, match="MIGRATOR_UNSAFE"):
                await upgrade(
                    owner, secrets.token_hex(24), runtime_role=runtime, migration_role=migrator
                )
            await owner.execute(
                sql.SQL("ALTER ROLE {} NOCREATEDB").format(sql.Identifier(migrator))
            )
            await owner.execute(
                sql.SQL("CREATE SCHEMA unexpected AUTHORIZATION {}").format(sql.Identifier(runtime))
            )
            with pytest.raises(RuntimeError, match="UNEXPECTED_OWNER"):
                await upgrade(
                    owner, secrets.token_hex(24), runtime_role=runtime, migration_role=migrator
                )
            await owner.execute("DROP SCHEMA unexpected")
            await owner.execute(
                sql.SQL("ALTER SCHEMA agent OWNER TO {}").format(sql.Identifier(runtime))
            )
            await owner.execute(
                sql.SQL("ALTER TABLE agent.execution_commands OWNER TO {}").format(
                    sql.Identifier(runtime)
                )
            )
            await owner.execute(
                "CREATE FUNCTION agent.legacy_function() RETURNS int LANGUAGE sql AS 'SELECT 1'"
            )
            await owner.execute(
                sql.SQL("ALTER FUNCTION agent.legacy_function() OWNER TO {}").format(
                    sql.Identifier(runtime)
                )
            )
            with pytest.raises(RuntimeError, match="REVIEW_OWNED_ROUTINES"):
                await upgrade(
                    owner, secrets.token_hex(24), runtime_role=runtime, migration_role=migrator
                )
            row = await (
                await owner.execute(
                    "SELECT pg_get_userbyid(c.relowner) AS owner FROM pg_class c "
                    "JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='agent' "
                    "AND c.relname='execution_commands'"
                )
            ).fetchone()
            assert row["owner"] == runtime
            await owner.execute("DROP FUNCTION agent.legacy_function()")
            await owner.execute("CREATE TYPE public.legacy_enum AS ENUM ('synthetic')")
            await owner.execute(
                sql.SQL("ALTER TYPE public.legacy_enum OWNER TO {}").format(sql.Identifier(runtime))
            )
            with pytest.raises(RuntimeError, match="REVIEW_OWNED_TYPES"):
                await upgrade(
                    owner, secrets.token_hex(24), runtime_role=runtime, migration_role=migrator
                )
            row = await (
                await owner.execute(
                    "SELECT pg_get_userbyid(c.relowner) AS owner FROM pg_class c "
                    "JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='agent' "
                    "AND c.relname='execution_commands'"
                )
            ).fetchone()
            assert row["owner"] == runtime
            await owner.execute("DROP TYPE public.legacy_enum")
            await upgrade(
                owner, secrets.token_hex(24), runtime_role=runtime, migration_role=migrator
            )
            # Existing migration login password is preserved despite a different supplied value.
            async with await psycopg.AsyncConnection.connect(
                migration_pool.conninfo, autocommit=True, row_factory=dict_row
            ) as connection:
                await grant_runtime(connection, role=runtime)
            async with AsyncConnectionPool(
                pool.conninfo,
                min_size=1,
                max_size=1,
                open=False,
                kwargs={"autocommit": True, "row_factory": dict_row},
            ) as restored:
                await restored.wait(timeout=10)
                await verify_runtime(restored, role=runtime)

    asyncio.run(scenario())


def test_standalone_function_and_type_ownership_fail_closed_in_any_schema():
    async def scenario():
        async with roles() as (pool, _, owner, runtime, _):
            definitions = (
                (
                    "CREATE FUNCTION public.standalone() RETURNS int LANGUAGE sql AS 'SELECT 1'",
                    "FUNCTION public.standalone()",
                ),
                ("CREATE TYPE public.standalone AS ENUM ('synthetic')", "TYPE public.standalone"),
            )
            for definition, target in definitions:
                await owner.execute(definition)
                await verify_runtime(pool, role=runtime)
                async with pool.connection() as connection:
                    with pytest.raises(psycopg.errors.InsufficientPrivilege):
                        await connection.execute("DROP " + target)
                await owner.execute(
                    sql.SQL("ALTER {} OWNER TO {}").format(sql.SQL(target), sql.Identifier(runtime))
                )
                with pytest.raises(RuntimeError, match="PRIVILEGES_UNSAFE"):
                    await verify_runtime(pool, role=runtime)
                await owner.execute("DROP " + target)
                await verify_runtime(pool, role=runtime)

    asyncio.run(scenario())


def test_readiness_refuses_privilege_expansion_and_missing_dml():
    async def scenario():
        async with roles() as (pool, migration_pool, owner, runtime, _):
            await verify_runtime(pool, role=runtime)
            async with migration_pool.connection() as connection:
                await connection.execute(
                    sql.SQL("REVOKE DELETE ON execution_commands FROM {}").format(
                        sql.Identifier(runtime)
                    )
                )
            with pytest.raises(RuntimeError, match="TABLE_GRANTS_INVALID"):
                await verify_runtime(pool, role=runtime)
            async with migration_pool.connection() as connection:
                await grant_runtime(connection, role=runtime)
            await owner.execute(
                sql.SQL("GRANT CREATE ON SCHEMA public TO {}").format(sql.Identifier(runtime))
            )
            with pytest.raises(RuntimeError, match="PRIVILEGES_UNSAFE"):
                await verify_runtime(pool, role=runtime)
            await owner.execute(
                sql.SQL("REVOKE CREATE ON SCHEMA public FROM {}").format(sql.Identifier(runtime))
            )
            async with migration_pool.connection() as connection:
                await connection.execute(
                    sql.SQL("GRANT TRUNCATE ON execution_commands TO {}").format(
                        sql.Identifier(runtime)
                    )
                )
            with pytest.raises(RuntimeError, match="TABLE_GRANTS_INVALID"):
                await verify_runtime(pool, role=runtime)

    asyncio.run(scenario())


def test_migrator_maintains_hnsw_while_runtime_seeds_and_searches_without_ddl():
    async def scenario():
        async with roles() as (pool, migration_pool, _, runtime, _):
            profile = "semantic-v1-" + "a" * 24
            tenant, version = await semantic_fixture(pool, count=40, profile=profile)
            exact = SemanticSearch(pool, SearchSettings())
            result = await exact.candidates(tenant, profile, [version], [1, 0, 0])
            assert result.mode == "exact" and len(result.rows) == 20
            async with await psycopg.AsyncConnection.connect(
                migration_pool.conninfo, autocommit=True, row_factory=dict_row
            ) as maintenance:
                created = await maintain_index(
                    maintenance, "create", tenant, profile, 3, confirm=True
                )
                assert created["state"] == "ready"
                assert await maintain_index(maintenance, "status", tenant, profile, 3) == created
                async with pool.connection() as connection:
                    for statement in (
                        "CREATE INDEX denied_semantic_index ON semantic_vectors(chunk_id)",
                        sql.SQL("DROP INDEX {}").format(sql.Identifier(created["index_name"])),
                    ):
                        with pytest.raises(psycopg.errors.InsufficientPrivilege):
                            await connection.execute(statement)
                approximate = await SemanticSearch(pool, SearchSettings("hnsw")).candidates(
                    tenant, profile, [version], [1, 0, 0]
                )
                assert approximate.mode == "hnsw" and approximate.reason == "eligible"
                assert len(approximate.rows) == 20
                assert all(
                    row["tenant_id"] == tenant and row["version_id"] == version
                    for row in approximate.rows
                )
                await verify_runtime(pool, role=runtime)
                assert (
                    await maintain_index(maintenance, "drop", tenant, profile, 3, confirm=True)
                )["state"] == "absent"
                assert (await maintain_index(maintenance, "status", tenant, profile, 3))[
                    "state"
                ] == "unavailable"
            fallback = await SemanticSearch(pool, SearchSettings("hnsw")).candidates(
                tenant, profile, [version], [1, 0, 0]
            )
            assert fallback.mode == "exact" and fallback.reason == "index_unavailable"
            assert [row["chunk_id"] for row in fallback.rows] == [
                row["chunk_id"] for row in result.rows
            ]

    asyncio.run(scenario())
