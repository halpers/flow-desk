"""Explicit tenant/profile HNSW maintenance and bounded opt-in semantic search."""

import argparse
import asyncio
import hashlib
import json
import os
import re
from dataclasses import dataclass
from uuid import UUID

import psycopg
from psycopg import sql
from psycopg.rows import dict_row

from flowdesk_agent.embeddings import vector_literal
from flowdesk_agent.tools.go_client import ToolError

MAX_HNSW_DIMENSION = 2000
CANDIDATE_LIMIT = 20
QUERY_TIMEOUT_MS = 5000
PROFILE_PATTERN = re.compile(r"semantic-v1-[0-9a-f]{24}\Z")


@dataclass(frozen=True)
class SearchSettings:
    mode: str = "exact"
    ef_search: int = 100
    max_scan_tuples: int = 20000

    def __post_init__(self):
        if (
            self.mode not in ("exact", "hnsw")
            or type(self.ef_search) is not int
            or not 40 <= self.ef_search <= 1000
            or type(self.max_scan_tuples) is not int
            or not 1000 <= self.max_scan_tuples <= 100000
        ):
            raise ToolError("SEMANTIC_SEARCH_CONFIG_INVALID")

    @classmethod
    def from_environment(cls, settings=None):
        config = os.environ if settings is None else settings
        try:
            return cls(
                config.get("SEMANTIC_SEARCH_MODE", "exact"),
                int(config.get("SEMANTIC_HNSW_EF_SEARCH", "100")),
                int(config.get("SEMANTIC_HNSW_MAX_SCAN_TUPLES", "20000")),
            )
        except (ValueError, TypeError) as error:
            raise ToolError("SEMANTIC_SEARCH_CONFIG_INVALID") from error


def identity(tenant_id, profile, dimension):
    try:
        tenant_id = UUID(str(tenant_id))
    except (ValueError, TypeError, AttributeError) as error:
        raise ToolError("SEMANTIC_INDEX_ARGUMENT_INVALID") from error
    if (
        not isinstance(profile, str)
        or not PROFILE_PATTERN.fullmatch(profile)
        or type(dimension) is not int
        or not 1 <= dimension <= MAX_HNSW_DIMENSION
    ):
        raise ToolError("SEMANTIC_INDEX_ARGUMENT_INVALID")
    digest = hashlib.sha256(f"{tenant_id}:{profile}:{dimension}".encode()).hexdigest()[:24]
    return tenant_id, "semantic_hnsw_" + digest


def normalized(expression):
    # Only compare server-produced fixed expressions with our fixed grammar.
    return re.sub(r"[\s()]", "", expression or "")


async def index_catalog(connection, name):
    return await (
        await connection.execute(
            "SELECT i.indisvalid,i.indisready,i.indisunique,i.indisprimary,"
            "i.indnatts,i.indnkeyatts,a.amname,n.nspname AS index_schema,"
            "tn.nspname AS table_schema,t.relname AS table_name,"
            "pg_get_expr(i.indexprs,i.indrelid) AS expression,"
            "pg_get_expr(i.indpred,i.indrelid) AS predicate,"
            "o.opcname,onsp.nspname AS operator_schema,r.reloptions "
            "FROM pg_catalog.pg_class r JOIN pg_catalog.pg_namespace n ON n.oid=r.relnamespace "
            "JOIN pg_catalog.pg_index i ON i.indexrelid=r.oid "
            "JOIN pg_catalog.pg_class t ON t.oid=i.indrelid "
            "JOIN pg_catalog.pg_namespace tn ON tn.oid=t.relnamespace "
            "JOIN pg_catalog.pg_am a ON a.oid=r.relam "
            "JOIN pg_catalog.pg_opclass o ON o.oid=i.indclass[0] "
            "JOIN pg_catalog.pg_namespace onsp ON onsp.oid=o.opcnamespace "
            "WHERE n.nspname=current_schema() AND r.relname=%s",
            (name,),
        )
    ).fetchone()


def catalog_matches(row, schema, tenant_id, profile, dimension):
    predicate = (
        f"tenant_id = '{tenant_id}'::uuid AND profile = '{profile}'::text "
        f"AND dimension = {dimension}"
    )
    return bool(
        row
        and row["index_schema"] == schema
        and row["table_schema"] == schema
        and row["table_name"] == "semantic_vectors"
        and row["amname"] == "hnsw"
        and row["indnatts"] == row["indnkeyatts"] == 1
        and not row["indisunique"]
        and not row["indisprimary"]
        and row["operator_schema"] == "knowledge"
        and row["opcname"] == "vector_cosine_ops"
        and normalized(row["expression"]) == normalized(f"embedding::knowledge.vector({dimension})")
        and normalized(row["predicate"]) == normalized(predicate)
        and sorted(row["reloptions"] or []) == ["ef_construction=64", "m=16"]
    )


def definition_hash(row):
    fields = (
        "index_schema",
        "table_schema",
        "table_name",
        "amname",
        "indnatts",
        "indnkeyatts",
        "indisunique",
        "indisprimary",
        "operator_schema",
        "opcname",
    )
    body = {key: row[key] for key in fields}
    body["expression"] = normalized(row["expression"])
    body["predicate"] = normalized(row["predicate"])
    body["reloptions"] = sorted(row["reloptions"] or [])
    return hashlib.sha256(json.dumps(body, sort_keys=True).encode()).hexdigest()


async def current_schema(connection):
    row = await (await connection.execute("SELECT current_schema() AS name")).fetchone()
    if not row["name"]:
        raise ToolError("SEMANTIC_INDEX_SCHEMA_INVALID")
    return row["name"]


async def maintain_index(connection, action, tenant_id, profile, dimension, *, confirm=False):
    """Use a dedicated autocommit connection; never a running executor's session."""
    tenant_id, name = identity(tenant_id, profile, dimension)
    if action not in ("create", "status", "drop") or (action != "status" and not confirm):
        raise ToolError("SEMANTIC_INDEX_CONFIRM_REQUIRED")
    if not connection.autocommit:
        raise ToolError("SEMANTIC_INDEX_AUTOCOMMIT_REQUIRED")
    schema = await current_schema(connection)
    lock_key = f"semantic-ann:{tenant_id}:{profile}:{dimension}"
    locked = await (
        await connection.execute(
            "SELECT pg_try_advisory_lock(hashtextextended(%s,0)) AS locked", (lock_key,)
        )
    ).fetchone()
    if not locked["locked"]:
        raise ToolError("SEMANTIC_INDEX_MAINTENANCE_BUSY")
    try:
        record = await (
            await connection.execute(
                "SELECT * FROM semantic_ann_indexes "
                "WHERE tenant_id=%s AND profile=%s AND dimension=%s",
                (tenant_id, profile, dimension),
            )
        ).fetchone()
        row = await index_catalog(connection, name)
        if record and record["index_name"] != name:
            raise ToolError("SEMANTIC_INDEX_DEFINITION_MISMATCH")
        if row and not catalog_matches(row, schema, tenant_id, profile, dimension):
            raise ToolError("SEMANTIC_INDEX_DEFINITION_MISMATCH")
        ready = bool(
            record
            and record["state"] == "ready"
            and row
            and row["indisvalid"]
            and row["indisready"]
            and record["definition_hash"] == definition_hash(row)
        )
        if action == "status":
            return {"state": "ready" if ready else "unavailable", "index_name": name}
        # These limits are session-local to this dedicated maintenance connection.
        await connection.execute("SET lock_timeout='5s'")
        await connection.execute("SET statement_timeout='300s'")
        if action == "drop":
            if row and not record:
                raise ToolError("SEMANTIC_INDEX_NOT_MANAGED")
            if record:
                await connection.execute(
                    "UPDATE semantic_ann_indexes SET state='building',definition_hash='' "
                    "WHERE tenant_id=%s AND profile=%s AND dimension=%s",
                    (tenant_id, profile, dimension),
                )
            if row:
                await connection.execute(
                    sql.SQL("DROP INDEX CONCURRENTLY {}").format(sql.Identifier(schema, name))
                )
            await connection.execute(
                "DELETE FROM semantic_ann_indexes WHERE tenant_id=%s "
                "AND profile=%s AND dimension=%s",
                (tenant_id, profile, dimension),
            )
            return {"state": "absent", "index_name": name}
        manifests = await (
            await connection.execute(
                "SELECT count(*) AS total,min(dimension) AS minimum,max(dimension) AS maximum "
                "FROM semantic_manifests WHERE tenant_id=%s AND profile=%s",
                (tenant_id, profile),
            )
        ).fetchone()
        if (
            not manifests["total"]
            or manifests["minimum"] != dimension
            or manifests["maximum"] != dimension
        ):
            raise ToolError("SEMANTIC_INDEX_SOURCE_NOT_READY")
        extension = await (
            await connection.execute("SELECT extversion FROM pg_extension WHERE extname='vector'")
        ).fetchone()
        if not extension or tuple(int(part) for part in extension["extversion"].split(".")[:2]) < (
            0,
            8,
        ):
            raise ToolError("SEMANTIC_INDEX_EXTENSION_UNSUPPORTED")
        await connection.execute(
            "INSERT INTO semantic_ann_indexes(tenant_id,profile,dimension,index_name,state) "
            "VALUES(%s,%s,%s,%s,'building') ON CONFLICT(tenant_id,profile,dimension) "
            "DO UPDATE SET state='building',definition_hash=''",
            (tenant_id, profile, dimension, name),
        )
        if row and not (row["indisvalid"] and row["indisready"]):
            await connection.execute(
                sql.SQL("DROP INDEX CONCURRENTLY {}").format(sql.Identifier(schema, name))
            )
            row = None
        if not row:
            await connection.execute(
                sql.SQL(
                    "CREATE INDEX CONCURRENTLY {} ON {} USING hnsw "
                    "((embedding::knowledge.vector({})) knowledge.vector_cosine_ops) "
                    "WITH (m=16,ef_construction=64) WHERE tenant_id={} "
                    "AND profile={} AND dimension={}"
                ).format(
                    sql.Identifier(name),
                    sql.Identifier(schema, "semantic_vectors"),
                    sql.SQL(str(dimension)),
                    sql.Literal(tenant_id),
                    sql.Literal(profile),
                    sql.Literal(dimension),
                )
            )
        row = await index_catalog(connection, name)
        if not catalog_matches(row, schema, tenant_id, profile, dimension) or not (
            row["indisvalid"] and row["indisready"]
        ):
            raise ToolError("SEMANTIC_INDEX_DEFINITION_MISMATCH")
        await connection.execute(
            "UPDATE semantic_ann_indexes SET state='ready',definition_hash=%s "
            "WHERE tenant_id=%s AND profile=%s AND dimension=%s",
            (definition_hash(row), tenant_id, profile, dimension),
        )
        return {"state": "ready", "index_name": name}
    finally:
        await connection.execute("SELECT pg_advisory_unlock(hashtextextended(%s,0))", (lock_key,))


def ann_query(tenant_id, profile, dimension):
    # Tenant/profile literals come from validated trusted context. The version array
    # remains inside the SQL candidate query, before LIMIT; never filter in Python.
    identity(tenant_id, profile, dimension)
    cast = sql.SQL("knowledge.vector({})").format(sql.SQL(str(dimension)))
    return sql.SQL(
        "WITH ann_candidates AS MATERIALIZED ("
        "SELECT s.chunk_id,s.version_id,s.tenant_id,(s.embedding::{cast}) "
        "OPERATOR(knowledge.<=>) %s::{cast} AS distance FROM semantic_vectors s "
        "WHERE s.tenant_id={tenant} AND s.profile={profile} AND s.dimension={dimension} "
        "AND s.version_id=ANY(%s) ORDER BY (s.embedding::{cast}) "
        "OPERATOR(knowledge.<=>) %s::{cast} LIMIT 20) "
        "SELECT c.*,1-a.distance AS rank FROM ann_candidates a JOIN knowledge_chunks c "
        "ON c.chunk_id=a.chunk_id AND c.version_id=a.version_id AND c.tenant_id=a.tenant_id "
        "ORDER BY a.distance,c.chunk_id"
    ).format(
        cast=cast,
        tenant=sql.Literal(UUID(str(tenant_id))),
        profile=sql.Literal(profile),
        dimension=sql.Literal(dimension),
    )


async def search_settings(connection, settings):
    for name, value in (
        ("plan_cache_mode", "force_custom_plan"),
        ("hnsw.ef_search", settings.ef_search),
        ("hnsw.iterative_scan", "strict_order"),
        ("hnsw.max_scan_tuples", settings.max_scan_tuples),
        ("statement_timeout", QUERY_TIMEOUT_MS),
    ):
        await connection.execute("SELECT set_config(%s,%s,true)", (name, str(value)))


@dataclass
class CandidateResult:
    rows: list
    mode: str
    reason: str


class SemanticSearch:
    def __init__(self, pool, settings=None):
        self.pool = pool
        self.settings = settings or SearchSettings.from_environment()

    query = (
        "SELECT c.*,1-(s.embedding OPERATOR(knowledge.<=>) "
        "%s::knowledge.vector) AS rank FROM semantic_vectors s JOIN knowledge_chunks c "
        "ON c.chunk_id=s.chunk_id AND c.version_id=s.version_id AND c.tenant_id=s.tenant_id "
        "WHERE s.tenant_id=%s AND s.profile=%s AND s.version_id=ANY(%s) "
        "ORDER BY (s.embedding OPERATOR(knowledge.<=>) %s::knowledge.vector)+0,"
        "c.chunk_id LIMIT 20"
    )

    async def exact(self, tenant_id, profile, versions, values):
        literal = vector_literal(values)
        async with self.pool.connection() as connection, connection.transaction():
            await connection.execute(
                "SELECT set_config('statement_timeout',%s,true)", (str(QUERY_TIMEOUT_MS),)
            )
            return await (
                await connection.execute(
                    self.query, (literal, tenant_id, profile, versions, literal)
                )
            ).fetchall()

    async def candidates(self, tenant_id, profile, versions, values):
        reason = "configured_exact"
        if self.settings.mode == "hnsw":
            if not 1 <= len(values) <= MAX_HNSW_DIMENSION:
                reason = "unsupported_dimension"
            else:
                tenant_id, name = identity(tenant_id, profile, len(values))
                async with self.pool.connection() as connection:
                    record = await (
                        await connection.execute(
                            "SELECT * FROM semantic_ann_indexes "
                            "WHERE tenant_id=%s AND profile=%s AND dimension=%s AND state='ready'",
                            (tenant_id, profile, len(values)),
                        )
                    ).fetchone()
                    row = await index_catalog(connection, name) if record else None
                    valid = bool(
                        record
                        and record["index_name"] == name
                        and row
                        and row["indisvalid"]
                        and row["indisready"]
                        and catalog_matches(
                            row, await current_schema(connection), tenant_id, profile, len(values)
                        )
                        and record["definition_hash"] == definition_hash(row)
                    )
                    if valid:
                        literal = vector_literal(values)
                        try:
                            async with connection.transaction():
                                await search_settings(connection, self.settings)
                                rows = await (
                                    await connection.execute(
                                        ann_query(tenant_id, profile, len(values)),
                                        (literal, versions, literal),
                                        prepare=False,
                                    )
                                ).fetchall()
                            if len(rows) == CANDIDATE_LIMIT:
                                return CandidateResult(rows, "hnsw", "eligible")
                            reason = "insufficient_candidates"
                        except psycopg.errors.QueryCanceled:
                            reason = "scan_timeout"
                    else:
                        reason = "index_unavailable"
        return CandidateResult(
            await self.exact(tenant_id, profile, versions, values), "exact", reason
        )


async def cli(arguments):
    dsn = os.getenv("AGENT_MIGRATION_DATABASE_URL")
    if not dsn:
        raise ToolError("SEMANTIC_INDEX_DATABASE_NOT_CONFIGURED")
    async with await psycopg.AsyncConnection.connect(
        dsn, autocommit=True, connect_timeout=5, row_factory=dict_row
    ) as connection:
        result = await maintain_index(
            connection,
            arguments.action,
            arguments.tenant_id,
            arguments.profile,
            arguments.dimension,
            confirm=arguments.confirm_maintenance,
        )
        print(json.dumps(result))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("status", "create", "drop"))
    parser.add_argument("--tenant-id", required=True, type=UUID)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--dimension", required=True, type=int)
    parser.add_argument("--confirm-maintenance", action="store_true")
    arguments = parser.parse_args()
    try:
        asyncio.run(cli(arguments))
    except ToolError as error:
        parser.exit(1, str(error) + "\n")
    except (psycopg.Error, OSError):
        parser.exit(1, "SEMANTIC_INDEX_MAINTENANCE_FAILED\n")


if __name__ == "__main__":
    main()
