"""Allowlisted run metadata. Never expose ledger payloads or checkpoints."""

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

from flowdesk_agent.tools.go_client import ToolError

OBSERVATION_LIMIT = 100
NODES = frozenset(
    (
        "understand_and_route",
        "read_business_tools",
        "finalize",
        "retrieve_knowledge",
        "allocate_input",
        "wait_input",
        "blocked",
        "allocate_action",
        "prepare_action",
        "wait_approval",
        "execute_action",
    )
)
TOOLS = frozenset(("order.lookup", "logistics.track", "ticket.search", "actions.execute"))
MODEL_ERRORS = frozenset(
    (
        "",
        "BUDGET_EXCEEDED",
        "MODEL_ATTEMPTS_EXHAUSTED",
        "MODEL_CALL_CONFLICT",
        "MODEL_CONFIG_INVALID",
        "MODEL_NOT_CONFIGURED",
        "MODEL_OUTPUT_INVALID",
        "MODEL_RATE_LIMITED",
        "MODEL_REJECTED",
        "MODEL_UNAVAILABLE",
    )
)


class Metadata(BaseModel):
    model_config = ConfigDict(extra="forbid")


class NodeObservation(Metadata):
    node: str = Field(max_length=64)
    status: Literal["completed", "failed", "interrupted", "cancelled"]
    duration_ms: int = Field(ge=0)
    generation: int = Field(ge=1)
    completed_at: str


class ModelObservation(Metadata):
    status: Literal["reserved", "completed", "failed", "unknown"]
    error_code: str = Field(max_length=64)
    duration_ms: int = Field(ge=0)
    duration_known: bool
    input_tokens: int = Field(ge=0)
    output_tokens: int = Field(ge=0)
    tokens_known: bool


class ToolObservation(Metadata):
    tool: str = Field(max_length=64)
    completed_count: int = Field(ge=0)


class RunUsage(Metadata):
    retrieval_attempts: int = 0
    model_attempts: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    model_tokens_known: bool = False
    embedding_attempts: int = 0
    embedding_input_tokens: int = 0
    embedding_tokens_known: bool = False
    cost_status: Literal["unknown"] = "unknown"


class AgentTraceData(Metadata):
    available: bool = False
    truncated: bool = False
    nodes: list[NodeObservation] = Field(default_factory=list, max_length=OBSERVATION_LIMIT)
    models: list[ModelObservation] = Field(default_factory=list, max_length=OBSERVATION_LIMIT)
    tools: list[ToolObservation] = Field(default_factory=list, max_length=OBSERVATION_LIMIT)
    usage: RunUsage = Field(default_factory=RunUsage)


async def bind_scope(pool, run_id, tenant_id):
    """Only called after Go has authenticated an execution and returned its tenant."""
    async with pool.connection() as conn:
        await conn.execute(
            "INSERT INTO run_observation_scopes(run_id,tenant_id) VALUES(%s,%s) "
            "ON CONFLICT(run_id) DO NOTHING",
            (run_id, tenant_id),
        )
        row = await (
            await conn.execute(
                "SELECT run_id FROM run_observation_scopes WHERE run_id=%s AND tenant_id=%s",
                (run_id, tenant_id),
            )
        ).fetchone()
        if not row:
            raise ToolError("OBSERVATION_SCOPE_CONFLICT")


async def read_trace(pool, run_id, tenant_id):
    async with pool.connection() as conn, conn.transaction():
        await conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        scope = await (
            await conn.execute(
                "SELECT run_id FROM run_observation_scopes WHERE run_id=%s AND tenant_id=%s",
                (run_id, tenant_id),
            )
        ).fetchone()
        if not scope:
            return AgentTraceData()
        # The immutable binding gates every lookup of this globally unique run ID.
        nodes = await (
            await conn.execute(
                "SELECT node_name,status,duration_ms,lease_generation,completed_at "
                "FROM execution_spans WHERE run_id=%s ORDER BY completed_at,span_id LIMIT %s",
                (run_id, OBSERVATION_LIMIT + 1),
            )
        ).fetchall()
        models = await (
            await conn.execute(
                "SELECT status,error_code,duration_ms,input_tokens,output_tokens "
                "FROM model_calls WHERE run_id=%s ORDER BY created_at,call_id LIMIT %s",
                (run_id, OBSERVATION_LIMIT + 1),
            )
        ).fetchall()
        tools = await (
            await conn.execute(
                "SELECT t.tool_name,count(*) AS completed_count FROM tool_results t "
                "JOIN execution_commands c ON c.dispatch_id=t.dispatch_id WHERE c.run_id=%s "
                "GROUP BY t.tool_name ORDER BY t.tool_name LIMIT %s",
                (run_id, OBSERVATION_LIMIT + 1),
            )
        ).fetchall()
        model_usage = await (
            await conn.execute(
                "SELECT count(*) AS attempts,coalesce(sum(input_tokens),0) AS input_tokens,"
                "coalesce(sum(output_tokens),0) AS output_tokens,"
                "coalesce(bool_and(input_tokens IS NOT NULL AND output_tokens IS NOT NULL),true) "
                "AS known FROM model_calls WHERE run_id=%s",
                (run_id,),
            )
        ).fetchone()
        embedding = await (
            await conn.execute(
                "SELECT count(*) AS attempts,coalesce(sum(input_tokens),0) AS input_tokens,"
                "coalesce(bool_and(input_tokens IS NOT NULL),true) AS known FROM embedding_calls "
                "WHERE tenant_id=%s AND operation=%s",
                (tenant_id, "query:" + str(run_id)),
            )
        ).fetchone()
        retrieval = await (
            await conn.execute(
                "SELECT count(*) AS attempts FROM retrieval_attempts WHERE run_id=%s",
                (run_id,),
            )
        ).fetchone()
    return AgentTraceData(
        available=True,
        truncated=any(len(rows) > OBSERVATION_LIMIT for rows in (nodes, models, tools)),
        nodes=[
            NodeObservation(
                node=row["node_name"] if row["node_name"] in NODES else "unknown",
                status=row["status"],
                duration_ms=row["duration_ms"],
                generation=row["lease_generation"],
                completed_at=row["completed_at"].isoformat(),
            )
            for row in nodes[:OBSERVATION_LIMIT]
        ],
        models=[
            ModelObservation(
                status=row["status"]
                if row["status"] in ("reserved", "completed", "failed")
                else "unknown",
                error_code=row["error_code"]
                if row["error_code"] in MODEL_ERRORS
                else "UNKNOWN_ERROR",
                duration_ms=row["duration_ms"] or 0,
                duration_known=row["duration_ms"] is not None,
                input_tokens=row["input_tokens"] or 0,
                output_tokens=row["output_tokens"] or 0,
                tokens_known=row["input_tokens"] is not None and row["output_tokens"] is not None,
            )
            for row in models[:OBSERVATION_LIMIT]
        ],
        tools=[
            ToolObservation(
                tool=row["tool_name"] if row["tool_name"] in TOOLS else "unknown",
                completed_count=row["completed_count"],
            )
            for row in tools[:OBSERVATION_LIMIT]
        ],
        usage=RunUsage(
            retrieval_attempts=retrieval["attempts"],
            model_attempts=model_usage["attempts"],
            input_tokens=model_usage["input_tokens"],
            output_tokens=model_usage["output_tokens"],
            model_tokens_known=model_usage["known"],
            embedding_attempts=embedding["attempts"],
            embedding_input_tokens=embedding["input_tokens"],
            embedding_tokens_known=embedding["known"],
        ),
    )
