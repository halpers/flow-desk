import hashlib
import json
from uuid import UUID, uuid4

from psycopg.types.json import Jsonb
from psycopg_pool import AsyncConnectionPool

from flowdesk_agent.api.schemas import ExecutionCommand


class CommandConflict(Exception):
    pass


class CommandStore:
    def __init__(self, pool: AsyncConnectionPool):
        self.pool = pool

    async def accept(self, command: ExecutionCommand):
        body = command.model_dump(mode="json")
        digest = hashlib.sha256(json.dumps(body, sort_keys=True).encode()).hexdigest()
        async with self.pool.connection() as conn, conn.transaction():
            await conn.execute(
                "INSERT INTO "
                "execution_commands(dispatch_id,run_id,lease_generation,command,request_hash,body)"
                " "
                "VALUES (%s,%s,%s,%s,%s,%s) ON CONFLICT DO NOTHING",
                (
                    command.dispatch_id,
                    command.run_id,
                    command.lease_generation,
                    command.command,
                    digest,
                    Jsonb(body),
                ),
            )
            row = await (
                await conn.execute(
                    "SELECT * FROM execution_commands WHERE dispatch_id=%s", (command.dispatch_id,)
                )
            ).fetchone()
            if not row or row["request_hash"] != digest:
                raise CommandConflict
            cancellation = await (
                await conn.execute(
                    "SELECT 1 FROM execution_cancellations WHERE run_id=%s",
                    (command.run_id,),
                )
            ).fetchone()
            if cancellation and row["status"] in ("queued", "running", "waiting"):
                await conn.execute(
                    "UPDATE execution_commands SET status='cancelled',lease_expires_at=NULL "
                    "WHERE dispatch_id=%s",
                    (command.dispatch_id,),
                )
                row["status"] = "cancelled"
            return row

    async def find(self, dispatch_id: UUID):
        async with self.pool.connection() as conn:
            return await (
                await conn.execute(
                    "SELECT * FROM execution_commands WHERE dispatch_id=%s", (dispatch_id,)
                )
            ).fetchone()

    async def claim(self):
        async with self.pool.connection() as conn, conn.transaction():
            row = await (
                await conn.execute(
                    "SELECT * FROM execution_commands WHERE "
                    "(status='queued' AND available_at<=now()) OR "
                    "(status='running' AND lease_expires_at<now()) "
                    "ORDER BY created_at,dispatch_id LIMIT 1 FOR UPDATE SKIP LOCKED"
                )
            ).fetchone()
            if not row:
                return None
            owner = uuid4()
            await conn.execute(
                "UPDATE execution_commands SET status='running',lease_owner=%s,"
                "lease_expires_at=now()+interval '30 "
                "seconds',attempt_count=attempt_count+1 WHERE dispatch_id=%s",
                (owner, row["dispatch_id"]),
            )
            row["lease_owner"] = owner
            row["attempt_count"] += 1
            return row

    async def heartbeat(self, dispatch_id: UUID, owner: UUID) -> bool:
        async with self.pool.connection() as conn:
            result = await conn.execute(
                "UPDATE execution_commands SET lease_expires_at=now()+interval '30 seconds' "
                "WHERE dispatch_id=%s AND lease_owner=%s AND status='running'",
                (dispatch_id, owner),
            )
            return result.rowcount == 1

    async def release(self, dispatch_id: UUID, owner: UUID, status: str, code: str = ""):
        async with self.pool.connection() as conn:
            await conn.execute(
                "UPDATE execution_commands SET status=%s,error_code=%s,lease_expires_at=NULL,"
                "available_at=now()+interval '2 seconds' "
                "WHERE dispatch_id=%s AND lease_owner=%s AND status='running'",
                (status, code, dispatch_id, owner),
            )

    async def own(self, conn, command: ExecutionCommand, owner: UUID):
        result = await conn.execute(
            "INSERT INTO "
            "execution_owners(run_id,current_generation,dispatch_id,worker_id) "
            "VALUES (%s,%s,%s,%s) "
            "ON CONFLICT(run_id) DO UPDATE SET current_generation=excluded.current_generation,"
            "dispatch_id=excluded.dispatch_id,worker_id=excluded.worker_id,"
            "ownership_version=execution_owners.ownership_version+1,updated_at=now() "
            "WHERE execution_owners.current_generation<=excluded.current_generation",
            (command.run_id, command.lease_generation, command.dispatch_id, owner),
        )
        return result.rowcount == 1

    async def still_owned(self, conn, dispatch_id: UUID, owner: UUID) -> bool:
        row = await (
            await conn.execute(
                "SELECT 1 FROM execution_commands WHERE dispatch_id=%s AND lease_owner=%s "
                "AND status='running' AND lease_expires_at>now() "
                "AND NOT EXISTS(SELECT 1 FROM execution_cancellations c "
                "WHERE c.run_id=execution_commands.run_id) "
                "AND EXISTS(SELECT 1 FROM execution_owners o "
                "WHERE o.run_id=execution_commands.run_id "
                "AND o.dispatch_id=execution_commands.dispatch_id "
                "AND o.worker_id=execution_commands.lease_owner "
                "AND o.current_generation=execution_commands.lease_generation)",
                (dispatch_id, owner),
            )
        ).fetchone()
        return row is not None

    async def cancel(self, command):
        async with self.pool.connection() as conn, conn.transaction():
            await conn.execute(
                "INSERT INTO execution_cancellations(run_id,dispatch_id,lease_generation) "
                "VALUES(%s,%s,%s) ON CONFLICT(run_id) DO NOTHING",
                (command.run_id, command.dispatch_id, command.lease_generation),
            )
            await conn.execute(
                "UPDATE execution_commands SET status='cancelled',lease_expires_at=NULL "
                "WHERE run_id=%s AND status IN ('queued','running','waiting')",
                (command.run_id,),
            )
