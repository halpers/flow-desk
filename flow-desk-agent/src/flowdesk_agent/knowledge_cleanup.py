"""Permanent retirement fence for Agent-owned indexes; business rules remain in Go."""

from flowdesk_agent.tools.go_client import ToolError


async def lock_version(connection, version_id):
    await connection.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended(%s,7717))", (str(version_id),)
    )


async def tombstone(connection, version_id):
    return await (
        await connection.execute(
            "SELECT * FROM knowledge_tombstones WHERE version_id=%s", (version_id,)
        )
    ).fetchone()


async def cleanup_index(pool, command):
    async with pool.connection() as connection, connection.transaction():
        await lock_version(connection, command.version_id)
        retired = await tombstone(connection, command.version_id)
        if retired:
            if any(
                str(retired[field]) != str(getattr(command, field))
                for field in ("tenant_id", "job_id", "content_hash")
            ):
                raise ToolError("CLEANUP_COMMAND_CONFLICT")
            return
        job = await (
            await connection.execute(
                "SELECT * FROM knowledge_jobs WHERE version_id=%s OR job_id=%s FOR UPDATE",
                (command.version_id, command.job_id),
            )
        ).fetchall()
        if any(
            row["version_id"] != command.version_id or row["job_id"] != command.job_id
            for row in job
        ):
            raise ToolError("CLEANUP_COMMAND_CONFLICT")
        manifest = await (
            await connection.execute(
                "SELECT * FROM knowledge_manifests WHERE version_id=%s FOR UPDATE",
                (command.version_id,),
            )
        ).fetchone()
        if manifest and (
            manifest["tenant_id"] != command.tenant_id
            or manifest["content_hash"] != command.content_hash
        ):
            raise ToolError("CLEANUP_COMMAND_CONFLICT")
        await connection.execute(
            "INSERT INTO knowledge_tombstones(version_id,tenant_id,job_id,content_hash) "
            "VALUES(%s,%s,%s,%s)",
            (command.version_id, command.tenant_id, command.job_id, command.content_hash),
        )
        await connection.execute(
            "UPDATE knowledge_jobs SET status='completed',attempts=attempts+1,"
            "lease_until=null,error_code='VERSION_RETIRED' WHERE version_id=%s",
            (command.version_id,),
        )
        for table in (
            "semantic_vectors",
            "semantic_manifests",
            "knowledge_chunks",
            "knowledge_manifests",
        ):
            await connection.execute(
                f"DELETE FROM {table} WHERE version_id=%s", (command.version_id,)
            )
