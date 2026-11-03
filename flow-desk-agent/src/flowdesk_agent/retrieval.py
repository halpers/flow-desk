"""Tenant-filtered lexical/character-vector baseline with fenced, durable indexing."""

import asyncio
import hashlib
import math
import re
from uuid import UUID, uuid5

from flowdesk_agent.tools.go_client import ToolError

PROFILE = "char-ngram-v1"
DIMENSION = 256


def terms(text):
    words = re.findall(r"[a-z0-9]+", text.lower())
    for segment in re.findall(r"[\u3400-\u9fff]+", text):
        words.extend(segment[index : index + 2] for index in range(max(1, len(segment) - 1)))
    return words


def vector(text):
    values = [0.0] * DIMENSION
    for word in terms(text):
        digest = hashlib.sha256(word.encode()).digest()
        values[int.from_bytes(digest[:4], "big") % DIMENSION] += 1.0 if digest[4] % 2 == 0 else -1.0
    norm = math.sqrt(sum(value * value for value in values)) or 1
    return "[" + ",".join(f"{value / norm:.8f}" for value in values) + "]"


def split_text(content):
    return [
        content[position : position + 700]
        for position in range(0, len(content), 600)
        if content[position : position + 700].strip()
    ]


class KnowledgeIndex:
    def __init__(self, pool, http):
        self.pool, self.http = pool, http

    async def accept(self, job, version):
        async with self.pool.connection() as conn:
            await conn.execute(
                "INSERT INTO knowledge_jobs(job_id,version_id) VALUES(%s,%s) "
                "ON CONFLICT(job_id) DO NOTHING",
                (job, version),
            )
            row = await (
                await conn.execute("SELECT version_id FROM knowledge_jobs WHERE job_id=%s", (job,))
            ).fetchone()
            if str(row["version_id"]) != str(version):
                raise ToolError("INDEX_COMMAND_CONFLICT")

    async def claim(self):
        async with self.pool.connection() as conn, conn.transaction():
            row = await (
                await conn.execute(
                    "SELECT * FROM knowledge_jobs WHERE "
                    "(status='pending' AND available_at<=now()) OR "
                    "(status='working' AND lease_until<now()) "
                    "ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1"
                )
            ).fetchone()
            if not row:
                return None
            return await (
                await conn.execute(
                    "UPDATE knowledge_jobs SET status='working',attempts=attempts+1,"
                    "lease_until=now()+interval '60 seconds' WHERE job_id=%s RETURNING *",
                    (row["job_id"],),
                )
            ).fetchone()

    async def process(self, job):
        response = await self.http.get(
            f"/internal/v1/knowledge/versions/{job['version_id']}/source",
            params={"job_id": str(job["job_id"])},
        )
        if response.status_code != 200:
            raise ToolError("INDEX_SOURCE_UNAVAILABLE")
        source = response.json()["data"]
        content = source["content"]
        if hashlib.sha256(content.encode()).hexdigest() != source["content_hash"]:
            raise ToolError("INDEX_HASH_MISMATCH")
        chunks = split_text(content)
        if not chunks or len(chunks) > 1000:
            raise ToolError("INDEX_CONTENT_INVALID")
        async with self.pool.connection() as conn, conn.transaction():
            # The command row is the transaction fence. An expired owner cannot overwrite
            # a successor's manifest or acknowledge its delivery.
            owned = await (
                await conn.execute(
                    "SELECT attempts FROM knowledge_jobs WHERE job_id=%s AND status='working' "
                    "AND attempts=%s AND lease_until>now() FOR UPDATE",
                    (job["job_id"], job["attempts"]),
                )
            ).fetchone()
            if not owned:
                return
            existing = await (
                await conn.execute(
                    "SELECT content_hash,profile FROM knowledge_manifests WHERE version_id=%s",
                    (job["version_id"],),
                )
            ).fetchone()
            if existing and (
                existing["content_hash"] != source["content_hash"] or existing["profile"] != PROFILE
            ):
                raise ToolError("INDEX_PROFILE_CONFLICT")
            if not existing:
                await conn.execute(
                    "INSERT INTO knowledge_manifests VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                    (
                        job["version_id"],
                        source["tenant_id"],
                        source["knowledge_base_id"],
                        source["document_id"],
                        source["content_hash"],
                        PROFILE,
                        DIMENSION,
                        "chars-700-overlap-100-v1",
                        len(chunks),
                    ),
                )
                for position, chunk in enumerate(chunks):
                    await conn.execute(
                        "INSERT INTO knowledge_chunks(chunk_id,version_id,tenant_id,position,"
                        "content,content_hash,lexical,embedding) "
                        "VALUES(%s,%s,%s,%s,%s,%s,to_tsvector('simple',%s),%s::knowledge.vector)",
                        (
                            uuid5(UUID(str(job["version_id"])), str(position)),
                            job["version_id"],
                            source["tenant_id"],
                            position,
                            chunk,
                            hashlib.sha256(chunk.encode()).hexdigest(),
                            " ".join(terms(chunk)),
                            vector(chunk),
                        ),
                    )
        completed = await self.http.post(
            f"/internal/v1/knowledge/versions/{job['version_id']}/complete",
            json={
                "job_id": str(job["job_id"]),
                "content_hash": source["content_hash"],
                "chunk_count": len(chunks),
                "status": "ready",
            },
        )
        if completed.status_code != 200:
            raise ToolError("INDEX_CALLBACK_UNAVAILABLE")
        async with self.pool.connection() as conn:
            await conn.execute(
                "UPDATE knowledge_jobs SET status='completed',lease_until=null "
                "WHERE job_id=%s AND attempts=%s AND status='working'",
                (job["job_id"], job["attempts"]),
            )

    async def loop(self):
        while True:
            job = None
            try:
                job = await self.claim()
                if job:
                    await self.process(job)
                    continue
            except asyncio.CancelledError:
                raise
            except Exception:
                # No upstream response body, prompt or connection string is logged.
                if job:
                    async with self.pool.connection() as conn:
                        await conn.execute(
                            "UPDATE knowledge_jobs SET status='pending',lease_until=null,"
                            "available_at=now()+interval '10 seconds' WHERE job_id=%s "
                            "AND attempts=%s AND status='working'",
                            (job["job_id"], job["attempts"]),
                        )
            await asyncio.sleep(0.5)


class Retriever:
    def __init__(self, pool, tools):
        self.pool, self.tools = pool, tools

    async def search(self, query, top_k=3):
        if not 1 <= top_k <= 5 or len(query) > 500:
            raise ToolError("RETRIEVAL_ARGUMENT_INVALID")
        context = await self.tools.authorize()
        scope_response = await self.tools.http.get(
            f"/internal/v1/runs/{context.run_id}/knowledge-scope",
            headers={"X-Run-Capability": context.capability},
        )
        if scope_response.status_code != 200:
            raise ToolError("KNOWLEDGE_SCOPE_REJECTED")
        scope = scope_response.json()["data"]
        if scope["tenant_id"] != str(context.tenant_id):
            raise ToolError("KNOWLEDGE_SCOPE_INVALID")
        versions = [UUID(item["version_id"]) for item in scope["versions"]]
        if not versions:
            return []
        chosen = await self.rank(query, context.tenant_id, versions, top_k)
        context = await self.tools.authorize()
        checked = await self.tools.http.post(
            f"/internal/v1/runs/{context.run_id}/knowledge/validate",
            headers={"X-Run-Capability": context.capability},
            json={
                "candidates": [
                    {
                        "version_id": str(item["version_id"]),
                        "chunk_id": str(item["chunk_id"]),
                        "excerpt": item["content"],
                        "content_hash": item["content_hash"],
                    }
                    for item in chosen
                ]
            },
        )
        if checked.status_code != 200:
            raise ToolError("EVIDENCE_REJECTED")
        return checked.json()["data"]["items"]

    async def rank(self, query, tenant_id, versions, top_k=5, mode="hybrid"):
        query_terms = list(dict.fromkeys(terms(query)))[:200]
        if not query_terms:
            return []
        async with self.pool.connection() as conn:
            # Access filters are applied before either candidate list is ranked.
            lexical = await (
                await conn.execute(
                    "SELECT c.*,ts_rank(c.lexical,to_tsquery('simple',%s)) AS rank "
                    "FROM knowledge_chunks c JOIN knowledge_manifests m USING(version_id) "
                    "WHERE c.tenant_id=%s AND c.version_id=ANY(%s) AND m.profile=%s "
                    "AND c.lexical @@ to_tsquery('simple',%s) "
                    "ORDER BY rank DESC,c.chunk_id LIMIT 20",
                    (
                        " | ".join(query_terms),
                        tenant_id,
                        versions,
                        PROFILE,
                        " | ".join(query_terms),
                    ),
                )
            ).fetchall()
            if mode == "lexical":
                return lexical[:top_k]
            semantic = await (
                await conn.execute(
                    "SELECT c.*,1-(embedding OPERATOR(knowledge.<=>) %s::knowledge.vector) AS rank "
                    "FROM knowledge_chunks c JOIN knowledge_manifests m USING(version_id) "
                    "WHERE c.tenant_id=%s AND c.version_id=ANY(%s) AND m.profile=%s "
                    "ORDER BY embedding OPERATOR(knowledge.<=>) %s::knowledge.vector,"
                    "c.chunk_id LIMIT 20",
                    (vector(query), tenant_id, versions, PROFILE, vector(query)),
                )
            ).fetchall()
        candidates, scores = {}, {}
        for ranking in (lexical, [item for item in semantic if item["rank"] >= 0.15]):
            for rank, item in enumerate(ranking, start=1):
                chunk = str(item["chunk_id"])
                candidates[chunk] = item
                scores[chunk] = scores.get(chunk, 0) + 1 / (60 + rank)
        chosen = [
            candidates[key] for key in sorted(scores, key=lambda key: (-scores[key], key))[:top_k]
        ]
        return chosen
