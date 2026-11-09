"""Lossless checkpoint tier changes; permanent execution fence survives restoration."""

from typing import Literal
from uuid import UUID

from psycopg import sql
from pydantic import BaseModel, ConfigDict, Field

from flowdesk_agent.storage.lifecycle import LIFECYCLE_COUNTS_SQL, PROTECTED_COUNTS
from flowdesk_agent.tools.go_client import ToolError

TABLES = ("checkpoints", "checkpoint_blobs", "checkpoint_writes")
MAX_HISTORY_ROWS = 100000


class HistoryCommand(BaseModel):
    model_config = ConfigDict(extra="forbid")
    operation_id: UUID
    tenant_id: UUID
    generation: int = Field(strict=True, ge=1, le=9007199254740991)
    target: Literal["archived", "live"]


class HistoryReceipt(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    operation_id: UUID
    run_id: UUID
    tenant_id: UUID
    generation: int = Field(ge=1, le=9007199254740991)
    target: Literal["archived", "live"]
    checkpoint_count: int = Field(ge=0, le=MAX_HISTORY_ROWS)
    checkpoint_blob_count: int = Field(ge=0, le=MAX_HISTORY_ROWS)
    checkpoint_write_count: int = Field(ge=0, le=MAX_HISTORY_ROWS)


async def change_history(pool, run_id: UUID, command: HistoryCommand):
    async with pool.connection() as conn, conn.transaction():
        await conn.execute("SET LOCAL statement_timeout='5s'")
        lock = await (
            await conn.execute(
                "SELECT pg_try_advisory_xact_lock(hashtextextended(%s,3)) AS locked",
                (str(run_id),),
            )
        ).fetchone()
        if not lock["locked"]:
            raise ToolError("RUN_HISTORY_EXECUTOR_ACTIVE")
        scope = await (
            await conn.execute(
                "SELECT 1 FROM run_observation_scopes WHERE run_id=%s AND tenant_id=%s FOR SHARE",
                (run_id, command.tenant_id),
            )
        ).fetchone()
        if not scope:
            raise ToolError("RUN_HISTORY_SCOPE_UNAVAILABLE")
        previous = await (
            await conn.execute(
                "SELECT * FROM run_history_receipts WHERE operation_id=%s",
                (command.operation_id,),
            )
        ).fetchone()
        if previous:
            if (
                any(
                    previous[key] != getattr(command, key)
                    for key in ("operation_id", "tenant_id", "generation", "target")
                )
                or previous["run_id"] != run_id
            ):
                raise ToolError("RUN_HISTORY_CONFLICT")
            return HistoryReceipt(**{key: previous[key] for key in HistoryReceipt.model_fields})
        fence = await (
            await conn.execute(
                "SELECT * FROM run_history_fences WHERE run_id=%s FOR UPDATE",
                (run_id,),
            )
        ).fetchone()
        expected = fence["generation"] + 1 if fence else 1
        state = fence["state"] if fence else "live"
        if (
            command.generation != expected
            or state == command.target
            or (fence and fence["tenant_id"] != command.tenant_id)
        ):
            raise ToolError("RUN_HISTORY_CONFLICT")
        # The same lock is required before command INSERT, so a new execution
        # cannot appear between quiescence validation and publication of fence.
        await conn.execute(
            "SELECT dispatch_id FROM execution_commands WHERE run_id=%s FOR UPDATE", (run_id,)
        )
        counts = await (
            await conn.execute(
                LIFECYCLE_COUNTS_SQL,
                {
                    "run_id": run_id,
                    "tenant_id": command.tenant_id,
                    "thread_id": "run:" + str(run_id),
                    "operation": "query:" + str(run_id),
                },
            )
        ).fetchone()
        if any(counts[field] for field, _ in PROTECTED_COUNTS):
            raise ToolError("RUN_HISTORY_PROTECTED")
        source_cold = state == "archived"
        sizes = []
        for table in TABLES:
            source = table + "_history" if source_cold else table
            row = await (
                await conn.execute(
                    sql.SQL("SELECT count(*) AS count FROM {} WHERE thread_id=%s").format(
                        sql.Identifier(source)
                    ),
                    ("run:" + str(run_id),),
                )
            ).fetchone()
            sizes.append(row["count"])
        if sum(sizes) > MAX_HISTORY_ROWS:
            raise ToolError("RUN_HISTORY_LIMIT")
        await conn.execute(
            "INSERT INTO run_history_fences(run_id,tenant_id,generation,operation_id,state) "
            "VALUES(%s,%s,%s,%s,%s) ON CONFLICT(run_id) DO UPDATE SET "
            "generation=excluded.generation,operation_id=excluded.operation_id,state=excluded.state",
            (run_id, command.tenant_id, command.generation, command.operation_id, command.target),
        )
        await conn.execute(
            "SELECT set_config('flowdesk.history_operation',%s,true)", (str(command.operation_id),)
        )
        for table in TABLES:
            source, destination = (
                (table + "_history", table) if source_cold else (table, table + "_history")
            )
            # Bytea remains bytea inside PostgreSQL. No graph state is decoded,
            # serialized as JSON or returned over HTTP by maintenance.
            await conn.execute(
                sql.SQL(
                    "WITH moved AS (DELETE FROM {} WHERE thread_id=%s RETURNING *) "
                    "INSERT INTO {} SELECT * FROM moved"
                ).format(sql.Identifier(source), sql.Identifier(destination)),
                ("run:" + str(run_id),),
            )
        receipt = HistoryReceipt(
            **command.model_dump(),
            run_id=run_id,
            checkpoint_count=sizes[0],
            checkpoint_blob_count=sizes[1],
            checkpoint_write_count=sizes[2],
        )
        await conn.execute(
            "INSERT INTO run_history_receipts(operation_id,run_id,tenant_id,generation,target,"
            "checkpoint_count,checkpoint_blob_count,checkpoint_write_count) "
            "VALUES(%s,%s,%s,%s,%s,%s,%s,%s)",
            tuple(receipt.model_dump().values()),
        )
        return receipt
