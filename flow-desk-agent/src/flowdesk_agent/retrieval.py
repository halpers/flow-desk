"""Filtered lexical/semantic retrieval with immutable profiles and fenced indexing."""

import asyncio
import base64
import hashlib
import json
import math
import os
import re
from uuid import UUID, uuid4, uuid5

from flowdesk_agent.ann_index import SemanticSearch
from flowdesk_agent.embeddings import configured_embedder
from flowdesk_agent.knowledge_cleanup import lock_version, tombstone
from flowdesk_agent.pdf import PDFError, PDFManifest, extract_pdf
from flowdesk_agent.semantic_index import SemanticIndex
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
    def __init__(self, pool, http, *, embedder=None):
        self.pool, self.http = pool, http
        self.embedder = embedder

    async def accept(self, job, version):
        async with self.pool.connection() as conn, conn.transaction():
            await lock_version(conn, version)
            retired = await tombstone(conn, version)
            if retired:
                if str(retired["job_id"]) != str(job):
                    raise ToolError("INDEX_COMMAND_CONFLICT")
                return
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
        async with self.pool.connection() as conn:
            current = await (
                await conn.execute(
                    "SELECT * FROM knowledge_jobs WHERE job_id=%s AND attempts=%s "
                    "AND status='working' AND lease_until>now()",
                    (job["job_id"], job["attempts"]),
                )
            ).fetchone()
        if not current:
            return
        job = current
        response = await self.http.get(
            f"/internal/v1/knowledge/versions/{job['version_id']}/source",
            params={"job_id": str(job["job_id"])},
        )
        if response.status_code != 200:
            raise ToolError("INDEX_SOURCE_UNAVAILABLE")
        source = response.json()["data"]
        raw = (
            base64.b64decode(source["source_base64"], validate=True)
            if source.get("source_format") == "pdf"
            else source["content"].encode()
        )
        if hashlib.sha256(raw).hexdigest() != source["content_hash"]:
            raise ToolError("INDEX_HASH_MISMATCH")
        if job.get("error_code"):
            await self.complete(job, source, error_code=job["error_code"])
            return
        async with self.pool.connection() as conn:
            manifest = await (
                await conn.execute(
                    "SELECT * FROM knowledge_manifests WHERE version_id=%s", (job["version_id"],)
                )
            ).fetchone()
        pages, parser = [], ""
        if source.get("source_format") == "pdf":
            try:
                parsed = (
                    PDFManifest.model_validate(
                        {
                            "parser_version": manifest["parser_version"],
                            "pages": manifest["page_texts"],
                        }
                    )
                    if manifest
                    else await extract_pdf(
                        raw, source.get("ocr_mode", "off"), source.get("pdf_layout", "plain")
                    )
                )
                pages = [page.model_dump() for page in parsed.pages]
                parser = parsed.parser_version
            except PDFError as error:
                async with self.pool.connection() as conn:
                    recorded = await conn.execute(
                        "UPDATE knowledge_jobs SET error_code=%s WHERE job_id=%s "
                        "AND attempts=%s AND status='working' AND lease_until>now()",
                        (error.code, job["job_id"], job["attempts"]),
                    )
                    if not recorded.rowcount:
                        return
                await self.complete(job, source, error_code=error.code)
                return
        chunks = (
            [(page["page_number"], text) for page in pages for text in split_text(page["text"])]
            if pages
            else [(0, text) for text in split_text(source["content"])]
        )
        if not chunks or len(chunks) > 1000:
            raise ToolError("INDEX_CONTENT_INVALID")
        async with self.pool.connection() as conn, conn.transaction():
            # The command row is the transaction fence. An expired owner cannot overwrite
            # a successor's manifest or acknowledge its delivery.
            await lock_version(conn, job["version_id"])
            if await tombstone(conn, job["version_id"]):
                return
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
                    "SELECT * FROM knowledge_manifests WHERE version_id=%s",
                    (job["version_id"],),
                )
            ).fetchone()
            if existing and (
                existing["content_hash"] != source["content_hash"] or existing["profile"] != PROFILE
            ):
                raise ToolError("INDEX_PROFILE_CONFLICT")
            if not existing:
                await conn.execute(
                    "INSERT INTO knowledge_manifests(version_id,tenant_id,knowledge_base_id,"
                    "document_id,content_hash,profile,dimension,splitter_version,chunk_count,"
                    "page_texts,parser_version) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb,%s)",
                    (
                        job["version_id"],
                        source["tenant_id"],
                        source["knowledge_base_id"],
                        source["document_id"],
                        source["content_hash"],
                        PROFILE,
                        DIMENSION,
                        "pages-chars-700-overlap-100-v1" if pages else "chars-700-overlap-100-v1",
                        len(chunks),
                        json.dumps(pages, ensure_ascii=False),
                        parser,
                    ),
                )
                for position, (page_number, chunk) in enumerate(chunks):
                    await conn.execute(
                        "INSERT INTO knowledge_chunks(chunk_id,version_id,tenant_id,position,"
                        "content,content_hash,lexical,embedding,page_number) "
                        "VALUES(%s,%s,%s,%s,%s,%s,to_tsvector('simple',%s),%s::knowledge.vector,%s)",
                        (
                            uuid5(UUID(str(job["version_id"])), str(position)),
                            job["version_id"],
                            source["tenant_id"],
                            position,
                            chunk,
                            hashlib.sha256(chunk.encode()).hexdigest(),
                            " ".join(terms(chunk)),
                            vector(chunk),
                            page_number,
                        ),
                    )
            else:
                # The committed manifest wins if a previous owner finished extraction.
                pages, parser = existing["page_texts"], existing["parser_version"]
        await self.complete(job, source, chunk_count=len(chunks), pages=pages, parser=parser)

    async def complete(self, job, source, *, chunk_count=0, pages=None, parser="", error_code=""):
        async def renew():
            async with self.pool.connection() as connection:
                renewed = await connection.execute(
                    "UPDATE knowledge_jobs SET lease_until=now()+interval '60 seconds' "
                    "WHERE job_id=%s AND attempts=%s AND status='working' AND lease_until>now()",
                    (job["job_id"], job["attempts"]),
                )
                if not renewed.rowcount:
                    raise ToolError("INDEX_LEASE_LOST")

        await renew()
        if self.embedder and not error_code:
            await SemanticIndex(self.pool, self.embedder).rebuild(
                job["version_id"], guard=renew, job=job
            )
            await renew()
        completed = await self.http.post(
            f"/internal/v1/knowledge/versions/{job['version_id']}/complete",
            json={
                "job_id": str(job["job_id"]),
                "content_hash": source["content_hash"],
                "chunk_count": chunk_count,
                "status": "failed" if error_code else "ready",
                "error_code": error_code,
                "page_texts": pages or [],
                "parser_version": parser,
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
    def __init__(
        self, pool, tools, *, embedder=None, profile=None, reranker=None, search_settings=None
    ):
        self.pool, self.tools = pool, tools
        self.profile = (
            profile if profile is not None else os.environ.get("RETRIEVAL_PROFILE", "baseline")
        )
        if self.profile not in ("baseline", "semantic"):
            raise ToolError("RETRIEVAL_CONFIG_INVALID")
        self.embedder = embedder
        if self.profile == "semantic" and self.embedder is None:
            self.embedder = configured_embedder(pool)
            if self.embedder is None:
                raise ToolError("EMBEDDING_NOT_CONFIGURED")
        self.reranker = reranker
        self.semantic_search = SemanticSearch(pool, search_settings)

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
        chosen = await self.rank(
            query,
            context.tenant_id,
            versions,
            5 if self.reranker else top_k,
            operation="query:" + str(context.run_id),
            guard=self.tools.authorize,
        )
        context = await self.tools.authorize()
        items = await self.validate(context, chosen)
        if not self.reranker or not items:
            return items[:top_k]
        # Only Go-revalidated excerpts may be sent to the external reranking model.
        by_chunk = {str(item["chunk_id"]): item for item in chosen}
        candidates = [
            {**by_chunk[item["chunk_id"]], "content": item["excerpt"]}
            for item in items
            if item.get("chunk_id") in by_chunk
        ]
        selected = await self.reranker.rank(query, candidates, self.profile)
        context = await self.tools.authorize()
        # Recheck revocation after the external call before returning any evidence.
        return await self.validate(context, selected[:top_k])

    async def validate(self, context, chosen):
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
                        "page_number": item.get("page_number", 0),
                    }
                    for item in chosen
                ]
            },
        )
        if checked.status_code != 200:
            raise ToolError("EVIDENCE_REJECTED")
        return checked.json()["data"]["items"]

    async def rank(
        self,
        query,
        tenant_id,
        versions,
        top_k=5,
        mode="hybrid",
        *,
        operation=None,
        guard=None,
    ):
        if not 1 <= top_k <= 5 or len(query) > 500 or mode not in ("lexical", "hybrid"):
            raise ToolError("RETRIEVAL_ARGUMENT_INVALID")
        query_terms = list(dict.fromkeys(terms(query)))[:200]
        if not query_terms and self.profile == "baseline":
            return []
        if not versions:
            return []
        async with self.pool.connection() as conn:
            # Access filters are applied before either candidate list is ranked.
            lexical = (
                await (
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
                if query_terms
                else []
            )
            if mode == "lexical":
                return lexical[:top_k]
            if self.profile == "baseline":
                semantic = await (
                    await conn.execute(
                        "SELECT c.*,1-(embedding OPERATOR(knowledge.<=>) "
                        "%s::knowledge.vector) AS rank "
                        "FROM knowledge_chunks c JOIN knowledge_manifests m USING(version_id) "
                        "WHERE c.tenant_id=%s AND c.version_id=ANY(%s) AND m.profile=%s "
                        "ORDER BY embedding OPERATOR(knowledge.<=>) %s::knowledge.vector,"
                        "c.chunk_id LIMIT 20",
                        (vector(query), tenant_id, versions, PROFILE, vector(query)),
                    )
                ).fetchall()
            else:
                owned = await (
                    await conn.execute(
                        "SELECT version_id FROM knowledge_manifests "
                        "WHERE tenant_id=%s AND version_id=ANY(%s)",
                        (tenant_id, versions),
                    )
                ).fetchall()
                manifests = await (
                    await conn.execute(
                        "SELECT version_id,dimension FROM semantic_manifests "
                        "WHERE tenant_id=%s AND profile=%s AND version_id=ANY(%s)",
                        (tenant_id, self.embedder.profile, versions),
                    )
                ).fetchall()
                if {row["version_id"] for row in owned} != {row["version_id"] for row in manifests}:
                    raise ToolError("RETRIEVAL_INDEX_NOT_READY")
                if not owned:
                    return []
                dimensions = {row["dimension"] for row in manifests}
        if self.profile == "semantic":
            values = (
                await self.embedder.encode(
                    tenant_id, [query], operation or "query:" + str(uuid4()), guard=guard
                )
            )[0]
            if dimensions != {len(values)}:
                raise ToolError("EMBEDDING_DIMENSION_CHANGED")
            result = await self.semantic_search.candidates(
                tenant_id, self.embedder.profile, versions, values
            )
            semantic = result.rows
        candidates, scores = {}, {}
        vector_candidates = (
            semantic
            if self.profile == "semantic"
            else [item for item in semantic if item["rank"] >= 0.15]
        )
        for ranking in (lexical, vector_candidates):
            for rank, item in enumerate(ranking, start=1):
                chunk = str(item["chunk_id"])
                candidates[chunk] = item
                scores[chunk] = scores.get(chunk, 0) + 1 / (60 + rank)
        chosen = [
            candidates[key] for key in sorted(scores, key=lambda key: (-scores[key], key))[:top_k]
        ]
        return chosen
