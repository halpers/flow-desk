"""Add immutable semantic profiles alongside the original, stable lexical chunks."""

from flowdesk_agent.embeddings import vector_literal
from flowdesk_agent.tools.go_client import ToolError


class SemanticIndex:
    def __init__(self, pool, embedder):
        self.pool, self.embedder = pool, embedder

    async def rebuild(self, version_id, *, guard=None, job=None):
        async with self.pool.connection() as connection:
            manifest = await (
                await connection.execute(
                    "SELECT * FROM knowledge_manifests WHERE version_id=%s", (version_id,)
                )
            ).fetchone()
            if not manifest:
                raise ToolError("INDEX_SOURCE_MISSING")
            existing = await (
                await connection.execute(
                    "SELECT content_hash FROM semantic_manifests "
                    "WHERE version_id=%s AND profile=%s",
                    (version_id, self.embedder.profile),
                )
            ).fetchone()
            if existing:
                if existing["content_hash"] != manifest["content_hash"]:
                    raise ToolError("INDEX_PROFILE_CONFLICT")
                return False
            chunks = await (
                await connection.execute(
                    "SELECT chunk_id,content FROM knowledge_chunks "
                    "WHERE version_id=%s AND tenant_id=%s ORDER BY position",
                    (version_id, manifest["tenant_id"]),
                )
            ).fetchall()
        if len(chunks) != manifest["chunk_count"] or not chunks:
            raise ToolError("INDEX_CONTENT_INVALID")
        vectors = await self.embedder.encode(
            manifest["tenant_id"],
            [chunk["content"] for chunk in chunks],
            "index:" + str(version_id),
            guard=guard,
        )
        if guard:
            await guard()
        async with self.pool.connection() as connection, connection.transaction():
            if job:
                owned = await (
                    await connection.execute(
                        "SELECT job_id FROM knowledge_jobs WHERE job_id=%s AND attempts=%s "
                        "AND status='working' AND lease_until>now() FOR UPDATE",
                        (job["job_id"], job["attempts"]),
                    )
                ).fetchone()
                if not owned:
                    raise ToolError("INDEX_LEASE_LOST")
            await connection.execute(
                "SELECT version_id FROM knowledge_manifests WHERE version_id=%s FOR UPDATE",
                (version_id,),
            )
            inserted = await connection.execute(
                "INSERT INTO semantic_manifests(version_id,profile,tenant_id,content_hash,"
                "model,dimension,chunk_count) VALUES(%s,%s,%s,%s,%s,%s,%s) "
                "ON CONFLICT(version_id,profile) DO NOTHING",
                (
                    version_id,
                    self.embedder.profile,
                    manifest["tenant_id"],
                    manifest["content_hash"],
                    self.embedder.model,
                    len(vectors[0]),
                    len(chunks),
                ),
            )
            if not inserted.rowcount:
                return False
            for chunk, values in zip(chunks, vectors, strict=True):
                await connection.execute(
                    "INSERT INTO semantic_vectors(chunk_id,version_id,profile,tenant_id,"
                    "dimension,embedding) VALUES(%s,%s,%s,%s,%s,%s::knowledge.vector)",
                    (
                        chunk["chunk_id"],
                        version_id,
                        self.embedder.profile,
                        manifest["tenant_id"],
                        len(values),
                        vector_literal(values),
                    ),
                )
        return True
