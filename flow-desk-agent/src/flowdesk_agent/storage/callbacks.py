import asyncio
from datetime import UTC, datetime
from uuid import UUID, uuid4, uuid5

from psycopg.types.json import Jsonb
from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.api.schemas import ExecutionCommand


class DeliveryFailed(Exception):
    pass


class CallbackStore:
    def __init__(self, pool: AsyncConnectionPool):
        self.pool = pool

    async def emit(
        self, command: ExecutionCommand, kind: str, payload: dict, tag: str = ""
    ) -> UUID:
        source = uuid5(command.dispatch_id, kind + ":" + tag)
        async with self.pool.connection() as conn, conn.transaction():
            await conn.execute(
                "SELECT dispatch_id FROM execution_commands WHERE dispatch_id=%s FOR UPDATE",
                (command.dispatch_id,),
            )
            existing = await (
                await conn.execute(
                    "SELECT source_event_id FROM callback_outbox WHERE source_event_id=%s",
                    (source,),
                )
            ).fetchone()
            if existing:
                return source
            position = await (
                await conn.execute(
                    "SELECT coalesce(max(position),0)+1 AS value FROM "
                    "callback_outbox WHERE dispatch_id=%s",
                    (command.dispatch_id,),
                )
            ).fetchone()
            body = {
                "source_event_id": str(source),
                "lease_generation": command.lease_generation,
                "type": kind,
                "occurred_at": datetime.now(UTC).isoformat(),
                "payload": payload,
            }
            await conn.execute(
                "INSERT INTO "
                "callback_outbox(source_event_id,dispatch_id,run_id,lease_generation,position,type,body)"
                " VALUES (%s,%s,%s,%s,%s,%s,%s)",
                (
                    source,
                    command.dispatch_id,
                    command.run_id,
                    command.lease_generation,
                    position["value"],
                    kind,
                    Jsonb(body),
                ),
            )
            return source

    async def claim(self):
        async with self.pool.connection() as conn, conn.transaction():
            row = await (
                await conn.execute(
                    "SELECT current.* FROM callback_outbox current WHERE "
                    "((current.status='pending' AND current.available_at<=now()) OR "
                    "(current.status='delivering' AND "
                    "current.lease_expires_at<now())) AND NOT EXISTS ("
                    "SELECT 1 FROM callback_outbox earlier WHERE earlier.run_id=current.run_id AND "
                    "(earlier.lease_generation,earlier.position)<(current.lease_generation,current.position)"
                    " "
                    "AND earlier.status<>'acked') ORDER BY current.created_at,current.position "
                    "LIMIT 1 FOR UPDATE OF current SKIP LOCKED"
                )
            ).fetchone()
            if not row:
                return None
            owner = uuid4()
            await conn.execute(
                "UPDATE callback_outbox SET status='delivering',lease_owner=%s,"
                "lease_expires_at=now()+interval '15 "
                "seconds',attempt_count=attempt_count+1 WHERE source_event_id=%s",
                (owner, row["source_event_id"]),
            )
            row["lease_owner"] = owner
            row["attempt_count"] += 1
            return row

    async def acknowledge(self, row: dict, seq: int):
        async with self.pool.connection() as conn:
            await conn.execute(
                "UPDATE callback_outbox SET "
                "status='acked',ack_seq=%s,lease_expires_at=NULL WHERE "
                "source_event_id=%s AND lease_owner=%s",
                (seq, row["source_event_id"], row["lease_owner"]),
            )

    async def retry(self, row: dict, code: str, permanent: bool = False):
        delay = min(10, 2 ** min(row["attempt_count"], 4))
        status = "permanent_failure" if permanent or row["attempt_count"] >= 20 else "pending"
        async with self.pool.connection() as conn:
            await conn.execute(
                "UPDATE callback_outbox SET "
                "status=%s,error_code=%s,available_at=now()+%s*interval '1 "
                "second',lease_expires_at=NULL "
                "WHERE source_event_id=%s AND lease_owner=%s",
                (status, code, delay, row["source_event_id"], row["lease_owner"]),
            )

    async def terminal(self, dispatch_id: UUID):
        async with self.pool.connection() as conn:
            return await (
                await conn.execute(
                    "SELECT * FROM callback_outbox WHERE dispatch_id=%s AND type "
                    "IN ('run.succeeded','run.failed') ORDER BY position DESC "
                    "LIMIT 1",
                    (dispatch_id,),
                )
            ).fetchone()

    async def wait(self, source: UUID, timeout: float = 120):
        async with asyncio.timeout(timeout):
            while True:
                async with self.pool.connection() as conn:
                    row = await (
                        await conn.execute(
                            "SELECT status FROM callback_outbox WHERE source_event_id=%s", (source,)
                        )
                    ).fetchone()
                if row and row["status"] == "acked":
                    return
                if row and row["status"] == "permanent_failure":
                    raise DeliveryFailed
                await asyncio.sleep(0.1)
