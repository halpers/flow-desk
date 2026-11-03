import asyncio
import hashlib
import json
from types import SimpleNamespace
from uuid import uuid4

import httpx
import pytest
from test_durable_runtime import isolated_pool

from flowdesk_agent.embeddings import EmbeddingClient
from flowdesk_agent.reranking import ModelReranker, Ranking
from flowdesk_agent.retrieval import KnowledgeIndex, Retriever
from flowdesk_agent.semantic_index import SemanticIndex
from flowdesk_agent.tools.go_client import ToolError

CONFIG = {
    "EMBEDDING_BASE_URL": "https://embedding.test/v1",
    "EMBEDDING_MODEL": "synthetic-embedding",
    "EMBEDDING_API_KEY": "synthetic-test-key",
    "EMBEDDING_DIMENSIONS": "3",
}


def test_embedding_cache_tenant_budget_retry_and_response_validation():
    async def scenario():
        async with isolated_pool() as pool:
            calls = []

            async def handle(request):
                body = json.loads(request.content)
                assert request.url.path == "/v1/embeddings"
                assert body["dimensions"] == 3 and body["encoding_format"] == "float"
                assert "tenant" not in body and "operation" not in body
                calls.append(body)
                if len(calls) == 1:
                    return httpx.Response(429)
                return httpx.Response(
                    200,
                    json={
                        "data": [
                            {"index": index, "embedding": [1.0, float(index), 0.0]}
                            for index in reversed(range(len(body["input"])))
                        ],
                        "usage": {"prompt_tokens": 7, "total_tokens": 7},
                    },
                )

            tenant, foreign = uuid4(), uuid4()
            client = EmbeddingClient(pool, settings=CONFIG, transport=httpx.MockTransport(handle))
            values = await client.encode(tenant, ["first", "second"], "query:one")
            assert values == [[1, 0, 0], [1, 1, 0]]
            assert await client.encode(tenant, ["second", "first"], "query:two") == values[::-1]
            assert len(calls) == 2
            await client.encode(foreign, ["first"], "query:one")
            assert len(calls) == 3
            for _ in range(8):
                await client.reserve(tenant, "query:budget", "synthetic-hash", 10)
            with pytest.raises(ToolError, match="EMBEDDING_BUDGET_EXCEEDED"):
                await client.reserve(tenant, "query:budget", "synthetic-hash", 10)
            async with pool.connection() as connection:
                ledger = await (
                    await connection.execute(
                        "SELECT status,input_tokens FROM embedding_calls "
                        "WHERE operation='query:one'"
                    )
                ).fetchall()
            assert sum(row["status"] == "completed" for row in ledger) == 2
            assert sum(row["input_tokens"] or 0 for row in ledger) == 14

            for malformed in (
                [{"index": 1, "embedding": [1.0, 0.0, 0.0]}],
                [{"index": 0, "embedding": [0.0, 0.0, 0.0]}],
                [{"index": 0, "embedding": [1.0, 0.0]}],
                [{"index": 0, "embedding": [True, 0.0, 0.0]}],
            ):

                async def invalid(request, data=malformed):
                    return httpx.Response(200, json={"data": data})

                broken = EmbeddingClient(
                    pool, settings=CONFIG, transport=httpx.MockTransport(invalid)
                )
                with pytest.raises(
                    ToolError, match="EMBEDDING_(RESPONSE_INVALID|DIMENSION_CHANGED)"
                ):
                    await broken.encode(tenant, [str(uuid4())], "query:" + str(uuid4()))

    asyncio.run(scenario())


def test_semantic_profile_fencing_filtering_and_idempotent_rebuild():
    async def scenario():
        async with isolated_pool() as pool:
            tenant, foreign = uuid4(), uuid4()
            sources, callback_count, embedding_count = {}, [], []
            own, other = uuid4(), uuid4()
            for version, owner, content in (
                (own, tenant, "合成政策：包裹延误超过三天可申请加急处理。"),
                (other, foreign, "合成政策：其他租户的特殊处理。"),
            ):
                sources[str(version)] = {
                    "version_id": str(version),
                    "tenant_id": str(owner),
                    "knowledge_base_id": str(uuid4()),
                    "document_id": str(uuid4()),
                    "content": content,
                    "content_hash": hashlib.sha256(content.encode()).hexdigest(),
                }

            async def embed(request):
                body = json.loads(request.content)
                embedding_count.append(body)
                return httpx.Response(
                    200,
                    json={
                        "data": [
                            {"index": index, "embedding": [1.0, 0.1, 0.0]}
                            for index in range(len(body["input"]))
                        ],
                    },
                )

            async def go(request):
                if request.url.path.endswith("/source"):
                    return httpx.Response(
                        200, json={"data": sources[request.url.path.split("/")[-2]]}
                    )
                callback_count.append(True)
                return httpx.Response(200, json={"data": {}})

            client = EmbeddingClient(pool, settings=CONFIG, transport=httpx.MockTransport(embed))
            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(go)
            ) as http:
                index = KnowledgeIndex(pool, http, embedder=client)
                for version in (own, other):
                    await index.accept(uuid4(), version)
                    job = await index.claim()
                    await index.process(job)
                    await index.process(job)
                assert len(embedding_count) == 2
                assert not await SemanticIndex(pool, client).rebuild(own)
                retriever = Retriever(pool, None, embedder=client, profile="semantic")
                result = await retriever.rank("能不能帮我催一下快递", tenant, [own, other])
                assert len(result) == 1 and result[0]["version_id"] == own
                changed = EmbeddingClient(
                    pool,
                    settings={**CONFIG, "EMBEDDING_REVISION": "2"},
                    transport=httpx.MockTransport(embed),
                )
                with pytest.raises(ToolError, match="RETRIEVAL_INDEX_NOT_READY"):
                    await Retriever(pool, None, embedder=changed, profile="semantic").rank(
                        "处理延误", tenant, [own]
                    )
                # A stale index attempt must not publish a new semantic profile.
                with pytest.raises(ToolError, match="INDEX_LEASE_LOST"):
                    await SemanticIndex(pool, changed).rebuild(
                        own,
                        job={
                            "job_id": job["job_id"],
                            "attempts": job["attempts"] - 1,
                        },
                    )
                async with pool.connection() as connection:
                    count = await (
                        await connection.execute(
                            "SELECT count(*) AS total FROM semantic_manifests WHERE profile=%s",
                            (changed.profile,),
                        )
                    ).fetchone()
                assert count["total"] == 0

    asyncio.run(scenario())


def test_reranker_rejects_fabricated_ids_and_preserves_candidate_order_on_failure():
    async def scenario():
        async with isolated_pool() as pool:
            rows = [
                {"chunk_id": uuid4(), "content": "合成相关政策"},
                {"chunk_id": uuid4(), "content": "合成干扰政策"},
            ]
            command = SimpleNamespace(run_id=uuid4())

            async def complete(*args):
                return Ranking(
                    scores=[
                        {"candidate_id": str(rows[0]["chunk_id"]), "score": 0.1},
                        {"candidate_id": str(rows[1]["chunk_id"]), "score": 0.9},
                    ]
                )

            model = SimpleNamespace(complete=complete, command=command)
            reranker = ModelReranker(pool, model)
            assert (await reranker.rank("问题", rows, "synthetic")) == rows[::-1]

            async def fabricated(*args):
                return Ranking(scores=[{"candidate_id": str(uuid4()), "score": 1}])

            model.complete = fabricated
            assert (await reranker.rank("问题", rows, "synthetic")) == rows
            async with pool.connection() as connection:
                attempts = await (
                    await connection.execute("SELECT status FROM retrieval_attempts")
                ).fetchall()
            assert {row["status"] for row in attempts} == {"reranked", "rerank_fallback"}

    asyncio.run(scenario())


def test_rerank_uses_only_currently_authorized_excerpts_and_rechecks_revocation():
    async def scenario():
        context = SimpleNamespace(run_id=uuid4(), tenant_id=uuid4(), capability="synthetic")
        version = uuid4()
        rows = [
            {
                "chunk_id": uuid4(),
                "version_id": version,
                "content": content,
                "content_hash": hashlib.sha256(content.encode()).hexdigest(),
            }
            for content in ("合成有效政策", "已经撤回的政策")
        ]
        checks, received = [], []

        async def authorize():
            return context

        async def handle(request):
            if request.url.path.endswith("knowledge-scope"):
                return httpx.Response(
                    200,
                    json={
                        "data": {
                            "tenant_id": str(context.tenant_id),
                            "versions": [{"version_id": str(version)}],
                        }
                    },
                )
            checks.append(json.loads(request.content))
            return httpx.Response(
                200,
                json={
                    "data": {
                        "items": [
                            {
                                "id": str(uuid4()),
                                "chunk_id": str(rows[0]["chunk_id"]),
                                "excerpt": rows[0]["content"],
                            }
                        ]
                        if len(checks) == 1
                        else []
                    }
                },
            )

        class Reranker:
            async def rank(self, query, candidates, profile):
                received.extend(candidates)
                return candidates

        async def rank(*args, **kwargs):
            return rows

        async with httpx.AsyncClient(
            base_url="https://go.test", transport=httpx.MockTransport(handle)
        ) as http:
            retriever = Retriever(
                None,
                SimpleNamespace(authorize=authorize, http=http),
                profile="baseline",
                reranker=Reranker(),
            )
            retriever.rank = rank
            assert await retriever.search("问题") == []
        assert len(received) == 1 and received[0]["content"] == rows[0]["content"]
        assert len(checks) == 2 and len(checks[1]["candidates"]) == 1

    asyncio.run(scenario())
