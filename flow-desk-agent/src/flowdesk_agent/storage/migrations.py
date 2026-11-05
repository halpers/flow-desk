"""Agent-owned schema only. No Go business table is read or written here."""

from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from psycopg_pool import AsyncConnectionPool

MIGRATION_LOCK = 60111102
MIGRATIONS = [
    """
    CREATE TABLE execution_commands (
        dispatch_id UUID PRIMARY KEY, run_id UUID NOT NULL,
        lease_generation BIGINT NOT NULL CHECK (lease_generation>0),
        command TEXT NOT NULL, request_hash TEXT NOT NULL, body JSONB NOT NULL,
        status TEXT NOT NULL DEFAULT 'queued'
            CHECK (status IN ('queued','running','completed','failed')),
        lease_owner UUID, lease_expires_at TIMESTAMPTZ,
        attempt_count INTEGER NOT NULL DEFAULT 0, available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        error_code TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        UNIQUE(run_id,lease_generation,command)
    );
    CREATE INDEX command_claim_idx ON execution_commands(status,available_at,lease_expires_at);
    CREATE TABLE execution_owners (
        run_id UUID PRIMARY KEY, current_generation BIGINT NOT NULL,
        dispatch_id UUID NOT NULL, worker_id UUID NOT NULL,
        ownership_version BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    CREATE TABLE callback_outbox (
        source_event_id UUID PRIMARY KEY,
        dispatch_id UUID NOT NULL REFERENCES execution_commands(dispatch_id),
        run_id UUID NOT NULL, lease_generation BIGINT NOT NULL, position BIGINT NOT NULL,
        type TEXT NOT NULL, body JSONB NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending'
            CHECK(status IN ('pending','delivering','acked','permanent_failure')),
        lease_owner UUID, lease_expires_at TIMESTAMPTZ, attempt_count INTEGER NOT NULL DEFAULT 0,
        available_at TIMESTAMPTZ NOT NULL DEFAULT now(), error_code TEXT NOT NULL DEFAULT '',
        ack_seq BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        UNIQUE(dispatch_id,position)
    );
    CREATE INDEX callback_claim_idx
        ON callback_outbox(status,available_at,run_id,lease_generation,position);
    CREATE TABLE tool_results (
        id UUID PRIMARY KEY, dispatch_id UUID NOT NULL REFERENCES execution_commands(dispatch_id),
        tool_name TEXT NOT NULL, arguments JSONB NOT NULL, result JSONB NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    """,
    """
    CREATE TABLE knowledge_jobs (
        job_id UUID PRIMARY KEY, version_id UUID NOT NULL UNIQUE,
        status TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0,
        lease_until TIMESTAMPTZ, available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    CREATE TABLE knowledge_manifests (
        version_id UUID PRIMARY KEY, tenant_id UUID NOT NULL, knowledge_base_id UUID NOT NULL,
        document_id UUID NOT NULL, content_hash TEXT NOT NULL,
        profile TEXT NOT NULL, dimension INTEGER NOT NULL, splitter_version TEXT NOT NULL,
        chunk_count INTEGER NOT NULL
    );
    CREATE TABLE knowledge_chunks (
        chunk_id UUID PRIMARY KEY,
        version_id UUID NOT NULL REFERENCES knowledge_manifests(version_id),
        tenant_id UUID NOT NULL, position INTEGER NOT NULL,
        content TEXT NOT NULL, content_hash TEXT NOT NULL,
        lexical TSVECTOR NOT NULL, embedding knowledge.vector(256) NOT NULL,
        UNIQUE(version_id,position)
    );
    CREATE INDEX knowledge_chunk_filter ON knowledge_chunks(tenant_id,version_id);
    CREATE INDEX knowledge_chunk_lexical ON knowledge_chunks USING gin(lexical);
    CREATE TABLE model_calls (
        call_id UUID PRIMARY KEY, run_id UUID NOT NULL, request_hash TEXT NOT NULL,
        model TEXT NOT NULL, profile TEXT NOT NULL, reserved_units INTEGER NOT NULL,
        input_tokens INTEGER, output_tokens INTEGER, output JSONB,
        status TEXT NOT NULL DEFAULT 'reserved', error_code TEXT NOT NULL DEFAULT '',
        cost_status TEXT NOT NULL DEFAULT 'unknown', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    CREATE INDEX model_call_run ON model_calls(run_id);
    """,
    """
    ALTER TABLE execution_commands DROP CONSTRAINT execution_commands_status_check;
    ALTER TABLE execution_commands ADD CHECK(
        status IN ('queued','running','waiting','completed','failed','cancelled'));
    CREATE TABLE execution_cancellations (
        run_id UUID PRIMARY KEY, dispatch_id UUID NOT NULL UNIQUE,
        lease_generation BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    """,
    """
    CREATE TABLE execution_spans (
        span_id UUID PRIMARY KEY, run_id UUID NOT NULL, dispatch_id UUID NOT NULL,
        lease_generation BIGINT NOT NULL, node_name TEXT NOT NULL,
        status TEXT NOT NULL CHECK(status IN ('completed','failed','interrupted','cancelled')),
        duration_ms BIGINT NOT NULL CHECK(duration_ms>=0),
        completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    CREATE INDEX execution_span_run ON execution_spans(run_id,completed_at);
    ALTER TABLE model_calls ADD COLUMN duration_ms BIGINT;
    ALTER TABLE model_calls ADD COLUMN completed_at TIMESTAMPTZ;
    """,
    """
    CREATE TABLE embedding_calls (
        call_id UUID PRIMARY KEY, tenant_id UUID NOT NULL, profile TEXT NOT NULL,
        operation TEXT NOT NULL, request_hash TEXT NOT NULL,
        reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes>0),
        status TEXT NOT NULL CHECK(status IN ('reserved','completed','failed')),
        input_tokens INTEGER, dimension INTEGER, duration_ms BIGINT,
        error_code TEXT NOT NULL DEFAULT '', cost_status TEXT NOT NULL DEFAULT 'unknown',
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(), completed_at TIMESTAMPTZ
    );
    CREATE INDEX embedding_call_budget ON embedding_calls(tenant_id,profile,operation);
    CREATE TABLE embedding_cache (
        tenant_id UUID NOT NULL, profile TEXT NOT NULL, content_hash TEXT NOT NULL,
        dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
        embedding knowledge.vector NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        PRIMARY KEY(tenant_id,profile,content_hash),
        CHECK(knowledge.vector_dims(embedding)=dimension)
    );
    CREATE TABLE semantic_manifests (
        version_id UUID NOT NULL REFERENCES knowledge_manifests(version_id),
        profile TEXT NOT NULL, tenant_id UUID NOT NULL, content_hash TEXT NOT NULL,
        model TEXT NOT NULL, dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
        chunk_count INTEGER NOT NULL CHECK(chunk_count>0),
        completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        PRIMARY KEY(version_id,profile)
    );
    CREATE INDEX semantic_manifest_filter ON semantic_manifests(tenant_id,profile,version_id);
    CREATE TABLE semantic_vectors (
        chunk_id UUID NOT NULL REFERENCES knowledge_chunks(chunk_id),
        version_id UUID NOT NULL, profile TEXT NOT NULL, tenant_id UUID NOT NULL,
        dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
        embedding knowledge.vector NOT NULL,
        PRIMARY KEY(chunk_id,profile),
        FOREIGN KEY(version_id,profile) REFERENCES semantic_manifests(version_id,profile),
        CHECK(knowledge.vector_dims(embedding)=dimension)
    );
    CREATE INDEX semantic_vector_filter ON semantic_vectors(tenant_id,profile,version_id);
    CREATE TABLE retrieval_attempts (
        attempt_id UUID PRIMARY KEY, run_id UUID NOT NULL, profile TEXT NOT NULL,
        status TEXT NOT NULL, candidate_count INTEGER NOT NULL,
        duration_ms BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    """,
    """
    ALTER TABLE execution_commands ADD COLUMN traceparent TEXT NOT NULL DEFAULT '';
    """,
    """
    ALTER TABLE knowledge_manifests ADD COLUMN page_texts JSONB NOT NULL DEFAULT '[]';
    ALTER TABLE knowledge_manifests ADD COLUMN parser_version TEXT NOT NULL DEFAULT '';
    ALTER TABLE knowledge_chunks ADD COLUMN page_number INTEGER NOT NULL DEFAULT 0
        CHECK(page_number BETWEEN 0 AND 50);
    ALTER TABLE knowledge_jobs ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
    """,
    """
    CREATE TABLE run_observation_scopes (
        run_id UUID PRIMARY KEY, tenant_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    CREATE INDEX run_observation_tenant ON run_observation_scopes(tenant_id,run_id);
    """,
    """
    CREATE TABLE knowledge_tombstones (
        version_id UUID PRIMARY KEY, tenant_id UUID NOT NULL, job_id UUID NOT NULL UNIQUE,
        content_hash TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
    """,
]


async def migrate(pool: AsyncConnectionPool) -> None:
    async with pool.connection() as conn:
        await conn.execute("SELECT pg_advisory_lock(%s)", (MIGRATION_LOCK,))
        try:
            await conn.execute(
                "CREATE TABLE IF NOT EXISTS schema_versions "
                "(version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"
            )
            for version, sql in enumerate(MIGRATIONS, start=1):
                existing = await (
                    await conn.execute(
                        "SELECT version FROM schema_versions WHERE version=%s", (version,)
                    )
                ).fetchone()
                if existing:
                    continue
                async with conn.transaction():
                    await conn.execute(sql, prepare=False)
                    await conn.execute(
                        "INSERT INTO schema_versions(version) VALUES (%s)", (version,)
                    )
            await AsyncPostgresSaver(pool).setup()
        finally:
            await conn.execute("SELECT pg_advisory_unlock(%s)", (MIGRATION_LOCK,))
