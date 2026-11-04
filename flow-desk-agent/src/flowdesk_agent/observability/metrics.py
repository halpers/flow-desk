"""Bounded aggregate gauges from Agent-owned tables. No tenant/run/model labels."""

SERIES = {
    "commands": ("execution_commands", "status"),
    "callbacks": ("callback_outbox", "status"),
    "index_jobs": ("knowledge_jobs", "status"),
    "model_calls": ("model_calls", "status"),
    "embedding_calls": ("embedding_calls", "status"),
    "nodes": ("execution_spans", "status"),
}
ALLOWED_STATUSES = {
    "queued",
    "running",
    "waiting",
    "completed",
    "failed",
    "cancelled",
    "pending",
    "delivering",
    "acked",
    "permanent_failure",
    "working",
    "reserved",
    "interrupted",
}


async def render(pool):
    from psycopg import sql

    lines = [
        "# HELP flowdesk_agent_up Agent metadata scrape completed.",
        "# TYPE flowdesk_agent_up gauge",
        "flowdesk_agent_up 1",
    ]
    async with pool.connection() as connection:
        # A slow scrape must not occupy a runtime connection indefinitely.
        async with connection.transaction():
            await connection.execute("SET LOCAL statement_timeout='2000ms'")
            for series, (table, column) in SERIES.items():
                rows = await (
                    await connection.execute(
                        sql.SQL("SELECT {} AS status,count(*) AS value FROM {} GROUP BY {}").format(
                            sql.Identifier(column), sql.Identifier(table), sql.Identifier(column)
                        )
                    )
                ).fetchall()
                name = "flowdesk_agent_" + series
                lines.extend(
                    [
                        f"# HELP {name} Retained aggregate records; may decrease after cleanup.",
                        f"# TYPE {name} gauge",
                    ]
                )
                counts = {row["status"]: row["value"] for row in rows}
                for status in sorted(ALLOWED_STATUSES):
                    lines.append(f'{name}{{status="{status}"}} {counts.get(status, 0)}')
            usage = await (
                await connection.execute(
                    "SELECT coalesce(sum(input_tokens),0) AS input,"
                    "coalesce(sum(output_tokens),0) AS output FROM model_calls"
                )
            ).fetchone()
            lines.extend(
                [
                    "# TYPE flowdesk_agent_model_tokens gauge",
                    f'flowdesk_agent_model_tokens{{direction="input"}} {usage["input"]}',
                    f'flowdesk_agent_model_tokens{{direction="output"}} {usage["output"]}',
                ]
            )
            embedding = await (
                await connection.execute(
                    "SELECT coalesce(sum(input_tokens),0) AS input FROM embedding_calls"
                )
            ).fetchone()
            lines.extend(
                [
                    "# TYPE flowdesk_agent_embedding_tokens gauge",
                    f"flowdesk_agent_embedding_tokens {embedding['input']}",
                ]
            )
            latency = await (
                await connection.execute(
                    "SELECT coalesce(percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms),0) "
                    "AS p95 FROM retrieval_attempts WHERE created_at>now()-interval '5 minutes'"
                )
            ).fetchone()
            lines.extend(
                [
                    "# TYPE flowdesk_agent_rerank_p95_milliseconds gauge",
                    f"flowdesk_agent_rerank_p95_milliseconds {latency['p95']}",
                ]
            )
            retrieval = await (
                await connection.execute(
                    "SELECT coalesce(percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms),0) "
                    "AS p95 FROM execution_spans WHERE node_name='retrieve_knowledge' "
                    "AND completed_at>now()-interval '5 minutes'"
                )
            ).fetchone()
            lines.extend(
                [
                    "# TYPE flowdesk_agent_retrieval_node_p95_milliseconds gauge",
                    f"flowdesk_agent_retrieval_node_p95_milliseconds {retrieval['p95']}",
                ]
            )
    return "\n".join(lines) + "\n"
