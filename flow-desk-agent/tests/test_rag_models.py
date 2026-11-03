import asyncio
import hashlib
import json
from types import SimpleNamespace
from uuid import uuid4

import httpx
import pytest
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.models import CompatibleModel
from flowdesk_agent.retrieval import KnowledgeIndex, Retriever
from flowdesk_agent.tools.go_client import ToolError


def test_index_replay_filtered_hybrid_retrieval():
    async def scenario():
        async with isolated_pool() as pool:
            tenant, foreign = uuid4(), uuid4()
            allowed, forbidden = uuid4(), uuid4()
            sources = {}
            for version, owner, text in (
                (allowed, tenant, "合成退货政策：签收后七天内可申请退货。"),
                (forbidden, foreign, "合成退货政策：其他租户秘密特例。"),
            ):
                sources[str(version)] = {
                    "version_id": str(version),
                    "tenant_id": str(owner),
                    "knowledge_base_id": str(uuid4()),
                    "document_id": str(uuid4()),
                    "content": text,
                    "content_hash": hashlib.sha256(text.encode()).hexdigest(),
                }
            callbacks = []
            scope = [allowed, forbidden]  # Even a bad scope must not bypass the SQL tenant filter.

            async def handle(request):
                if request.url.path.endswith("/source"):
                    return httpx.Response(
                        200, json={"data": sources[request.url.path.split("/")[-2]]}
                    )
                if request.url.path.endswith("/complete"):
                    callbacks.append(json.loads(request.content))
                    if len(callbacks) == 1:
                        return httpx.Response(503)
                    return httpx.Response(200, json={"data": {}})
                if request.url.path.endswith("knowledge-scope"):
                    return httpx.Response(
                        200,
                        json={
                            "data": {
                                "tenant_id": str(tenant),
                                "versions": [{"version_id": str(version)} for version in scope],
                            }
                        },
                    )
                candidates = json.loads(request.content)["candidates"]
                assert all(item["version_id"] == str(allowed) for item in candidates)
                return httpx.Response(
                    200,
                    json={
                        "data": {
                            "items": [
                                {"id": item["chunk_id"], "excerpt": item["excerpt"]}
                                for item in candidates
                            ]
                        }
                    },
                )

            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(handle)
            ) as http:
                index = KnowledgeIndex(pool, http)
                for version in (allowed, forbidden):
                    job = uuid4()
                    await index.accept(job, version)
                    await index.accept(job, version)
                    claimed = await index.claim()
                    if version == allowed:
                        with pytest.raises(ToolError, match="INDEX_CALLBACK_UNAVAILABLE"):
                            await index.process(claimed)
                    await index.process(claimed)
                    await index.process(claimed)  # Replay must not duplicate manifest/chunks.
                context = SimpleNamespace(run_id=uuid4(), tenant_id=tenant, capability="test")

                async def authorize():
                    return context

                retriever = Retriever(
                    pool, SimpleNamespace(http=http, authorize=authorize), profile="baseline"
                )
                result = await retriever.search("退货期限")
                assert len(result) == 1 and "七天" in result[0]["excerpt"]
                scope[:] = []
                assert await retriever.search("退货期限") == []
                assert len(callbacks) == 3
                async with pool.connection() as conn:
                    row = await (
                        await conn.execute("SELECT count(*) AS count FROM knowledge_chunks")
                    ).fetchone()
                    assert row["count"] == 2

    asyncio.run(scenario())


def test_model_protocol_retry_cache_budget_and_invalid_output():
    async def scenario():
        async with isolated_pool() as pool:
            calls = []

            async def handle(request):
                payload = json.loads(request.content)
                assert request.url.path == "/v1/chat/completions"
                assert payload["response_format"] == {"type": "json_object"}
                assert "capability" not in json.dumps(payload)
                calls.append(payload)
                if len(calls) == 1:
                    return httpx.Response(429)
                return httpx.Response(
                    200,
                    json={
                        "choices": [
                            {
                                "finish_reason": "stop",
                                "message": {
                                    "content": json.dumps(
                                        {
                                            "order_no": "FD202611020002",
                                            "requested_action": "query",
                                            "knowledge_query": "",
                                        }
                                    )
                                },
                            }
                        ],
                        "usage": {"prompt_tokens": 100, "completion_tokens": 30},
                    },
                )

            settings = {
                "MODEL_BASE_URL": "https://model.test/v1",
                "MODEL_API_KEY": "synthetic-test-key",
                "MODEL_NAME": "synthetic-chat",
            }
            item = command()
            model = CompatibleModel(
                pool, item, transport=httpx.MockTransport(handle), settings=settings
            )
            assert (await model.plan("查询 FD202611020002")).order_no == "FD202611020002"
            assert (await model.plan("查询 FD202611020002")).order_no == "FD202611020002"
            assert len(calls) == 2
            with pytest.raises(ToolError, match="BUDGET_EXCEEDED"):
                await model.reserve(uuid4(), "synthetic-hash", 24001)
            async with pool.connection() as conn:
                row = await (
                    await conn.execute(
                        "SELECT count(*) AS calls,sum(input_tokens) AS tokens FROM model_calls"
                    )
                ).fetchone()
                assert row["calls"] == 2 and row["tokens"] == 100

            async def invalid(request):
                return httpx.Response(
                    200,
                    json={"choices": [{"finish_reason": "stop", "message": {"content": "   "}}]},
                )

            broken = CompatibleModel(
                pool, command(), transport=httpx.MockTransport(invalid), settings=settings
            )
            with pytest.raises(ToolError, match="MODEL_OUTPUT_INVALID"):
                await broken.plan("查询订单")

    asyncio.run(scenario())
