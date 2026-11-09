"""Read-only runtime counts behind Go-established tenant/run bindings.

No payload, model result, checkpoint JSON, or serialized graph state is loaded.
Even an observation without protection reasons cannot authorize deletion: it is
a transient snapshot and does not acquire the executor's advisory lock.
"""

from datetime import UTC
from uuid import UUID

from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.api.schemas import AgentLifecycleData, LifecycleProtectionReason

LIFECYCLE_QUERY_TIMEOUT_MS = 2500

LIFECYCLE_COUNTS_SQL = """
WITH command_counts AS (
    SELECT count(*) AS command_count,
           count(*) FILTER (WHERE status='queued') AS queued_command_count,
           count(*) FILTER (WHERE status='running') AS running_command_count,
           count(*) FILTER (WHERE status='waiting') AS waiting_command_count,
           count(*) FILTER (WHERE lease_expires_at>transaction_timestamp())
               AS live_command_lease_count
    FROM execution_commands WHERE run_id=%(run_id)s
), callback_counts AS (
    SELECT count(*) AS callback_count,
           count(*) FILTER (WHERE status<>'acked') AS unacknowledged_callback_count,
           count(*) FILTER (WHERE lease_expires_at>transaction_timestamp())
               AS live_callback_lease_count
    FROM callback_outbox WHERE run_id=%(run_id)s
), model_counts AS (
    SELECT count(*) AS model_call_count,
           count(*) FILTER (WHERE status='reserved') AS reserved_model_call_count,
           count(*) FILTER (WHERE status NOT IN ('reserved','completed','failed'))
               AS unknown_model_state_count
    FROM model_calls WHERE run_id=%(run_id)s
), embedding_counts AS (
    SELECT count(*) AS embedding_call_count,
           count(*) FILTER (WHERE status='reserved') AS reserved_embedding_call_count
    FROM embedding_calls WHERE tenant_id=%(tenant_id)s AND operation=%(operation)s
)
SELECT command_count, queued_command_count, running_command_count, waiting_command_count,
       live_command_lease_count, callback_count, unacknowledged_callback_count,
       live_callback_lease_count, model_call_count, reserved_model_call_count,
       unknown_model_state_count, embedding_call_count, reserved_embedding_call_count,
       ((SELECT count(*) FROM checkpoints WHERE thread_id=%(thread_id)s)
        +(SELECT count(*) FROM checkpoints_history WHERE thread_id=%(thread_id)s))
           AS checkpoint_count,
       ((SELECT count(*) FROM checkpoint_blobs WHERE thread_id=%(thread_id)s)
        +(SELECT count(*) FROM checkpoint_blobs_history WHERE thread_id=%(thread_id)s))
           AS checkpoint_blob_count,
       ((SELECT count(*) FROM checkpoint_writes WHERE thread_id=%(thread_id)s)
        +(SELECT count(*) FROM checkpoint_writes_history WHERE thread_id=%(thread_id)s))
           AS checkpoint_write_count,
       (SELECT count(*) FROM tool_results t
        JOIN execution_commands c ON c.dispatch_id=t.dispatch_id
        WHERE c.run_id=%(run_id)s) AS tool_result_count,
       (SELECT count(*) FROM execution_spans WHERE run_id=%(run_id)s) AS execution_span_count,
       (SELECT count(*) FROM retrieval_attempts WHERE run_id=%(run_id)s) AS retrieval_attempt_count
FROM command_counts CROSS JOIN callback_counts CROSS JOIN model_counts CROSS JOIN embedding_counts
"""

PROTECTED_COUNTS: tuple[tuple[str, LifecycleProtectionReason], ...] = (
    ("queued_command_count", "COMMAND_QUEUED"),
    ("running_command_count", "COMMAND_RUNNING"),
    ("waiting_command_count", "COMMAND_WAITING"),
    ("live_command_lease_count", "COMMAND_LEASE_ACTIVE"),
    ("unacknowledged_callback_count", "CALLBACK_UNACKNOWLEDGED"),
    ("live_callback_lease_count", "CALLBACK_LEASE_ACTIVE"),
    ("reserved_model_call_count", "MODEL_RESERVED"),
    ("unknown_model_state_count", "MODEL_STATE_UNKNOWN"),
    ("reserved_embedding_call_count", "EMBEDDING_RESERVED"),
)


async def read_lifecycle(pool: AsyncConnectionPool, run_id: UUID, tenant_id: UUID):
    # Only a UUID from validated HTTP input reaches this boundary; never accept
    # a caller-selected thread, tenant search_path, SQL, or model argument.
    async with pool.connection() as conn, conn.transaction():
        await conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        await conn.execute(
            "SELECT set_config('statement_timeout', %s, true)",
            (str(LIFECYCLE_QUERY_TIMEOUT_MS),),
        )
        scope = await (
            await conn.execute(
                "SELECT transaction_timestamp() AS observed_at, EXISTS("
                "SELECT 1 FROM run_observation_scopes WHERE run_id=%s AND tenant_id=%s) AS bound",
                (run_id, tenant_id),
            )
        ).fetchone()
        observed_at = scope["observed_at"].astimezone(UTC).isoformat()
        if not scope["bound"]:
            # A pending execution may not have an authenticated tenant binding
            # yet. Missing and foreign-tenant scopes deliberately look alike.
            return AgentLifecycleData(
                available=False,
                observed_at=observed_at,
                observation_only=True,
                protection_reasons=["AGENT_SCOPE_UNAVAILABLE"],
                **{
                    field: 0
                    for field in AgentLifecycleData.model_fields
                    if field.endswith("_count")
                },
            )
        counts = await (
            await conn.execute(
                LIFECYCLE_COUNTS_SQL,
                {
                    "run_id": run_id,
                    "tenant_id": tenant_id,
                    "thread_id": "run:" + str(run_id),
                    "operation": "query:" + str(run_id),
                },
            )
        ).fetchone()
        protections = [reason for field, reason in PROTECTED_COUNTS if counts[field]]
        # Never serialize an unrestricted database row. The internal unknown
        # status count is represented only by an allowlisted protection flag.
        return AgentLifecycleData(
            available=True,
            observed_at=observed_at,
            observation_only=True,
            protection_reasons=protections,
            **{
                field: counts[field]
                for field in AgentLifecycleData.model_fields
                if field.endswith("_count")
            },
        )
