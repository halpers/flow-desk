import asyncio
import hashlib
from uuid import uuid4

import httpx
import pytest
from test_durable_runtime import isolated_pool

from flowdesk_agent.api.schemas import CleanupCommand
from flowdesk_agent.knowledge_cleanup import cleanup_index
from flowdesk_agent.pdf import PDFManifest
from flowdesk_agent.pdf_limits import PARSER_VERSION
from flowdesk_agent.retrieval import KnowledgeIndex
from flowdesk_agent.semantic_index import SemanticIndex
from flowdesk_agent.tools.go_client import ToolError


def fixture_source():
    version, job, tenant = uuid4(), uuid4(), uuid4()
    content = "Synthetic version retention policy"
    source = dict(
        version_id=str(version),
        tenant_id=str(tenant),
        knowledge_base_id=str(uuid4()),
        document_id=str(uuid4()),
        content=content,
        content_hash=hashlib.sha256(content.encode()).hexdigest(),
    )
    command = CleanupCommand(
        version_id=version, tenant_id=tenant, job_id=job, content_hash=source["content_hash"]
    )
    return source, command


async def assert_empty(pool, command):
    async with pool.connection() as connection:
        for table in (
            "knowledge_manifests",
            "knowledge_chunks",
            "semantic_manifests",
            "semantic_vectors",
        ):
            row = await (
                await connection.execute(
                    f"SELECT count(*) AS count FROM {table} WHERE version_id=%s",
                    (command.version_id,),
                )
            ).fetchone()
            assert row["count"] == 0, table
        row = await (
            await connection.execute(
                "SELECT count(*) AS count FROM knowledge_tombstones WHERE version_id=%s",
                (command.version_id,),
            )
        ).fetchone()
        assert row["count"] == 1


def test_cleanup_atomic_deletion_conflicts_replay_and_late_delivery():
    async def scenario():
        async with isolated_pool() as pool:
            source, command = fixture_source()

            async def handle(request):
                return httpx.Response(200, json={"data": source if request.method == "GET" else {}})

            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(handle)
            ) as http:
                index = KnowledgeIndex(pool, http)
                await index.accept(command.job_id, command.version_id)
                await index.process(await index.claim())
                wrong = command.model_copy(update={"tenant_id": uuid4()})
                with pytest.raises(ToolError, match="CLEANUP_COMMAND_CONFLICT"):
                    await cleanup_index(pool, wrong)
                # Fail after the tombstone/job changes: the entire deletion must roll back.
                async with pool.connection() as connection:
                    await connection.execute(
                        """
                        CREATE FUNCTION fail_cleanup() RETURNS trigger LANGUAGE plpgsql AS
                        $$ BEGIN RAISE EXCEPTION 'synthetic delete failure'; END $$;
                        CREATE TRIGGER fail_cleanup BEFORE DELETE ON knowledge_chunks
                        FOR EACH ROW EXECUTE FUNCTION fail_cleanup();
                    """,
                        prepare=False,
                    )
                with pytest.raises(Exception, match="synthetic delete failure"):
                    await cleanup_index(pool, command)
                async with pool.connection() as connection:
                    row = await (
                        await connection.execute(
                            "SELECT count(*) AS count FROM knowledge_tombstones"
                        )
                    ).fetchone()
                    assert row["count"] == 0
                    row = await (
                        await connection.execute("SELECT count(*) AS count FROM knowledge_chunks")
                    ).fetchone()
                    assert row["count"] == 1
                    await connection.execute("DROP TRIGGER fail_cleanup ON knowledge_chunks")
                await cleanup_index(pool, command)
                await cleanup_index(pool, command)  # Lost response/restart replay.
                await KnowledgeIndex(pool, http).accept(command.job_id, command.version_id)
                with pytest.raises(ToolError, match="INDEX_COMMAND_CONFLICT"):
                    await index.accept(uuid4(), command.version_id)
                with pytest.raises(ToolError, match="CLEANUP_COMMAND_CONFLICT"):
                    await cleanup_index(pool, wrong)
                assert await index.claim() is None
                await assert_empty(pool, command)
                _, absent = fixture_source()
                await cleanup_index(pool, absent)
                await index.accept(absent.job_id, absent.version_id)
                assert await index.claim() is None
                await assert_empty(pool, absent)

    asyncio.run(scenario())


def test_cleanup_fences_inflight_pdf_parser(monkeypatch):
    async def scenario():
        async with isolated_pool() as pool:
            source, command = fixture_source()
            raw = b"synthetic PDF bytes"
            import base64

            source.update(
                source_format="pdf",
                source_base64=base64.b64encode(raw).decode(),
                content_hash=hashlib.sha256(raw).hexdigest(),
            )
            command = command.model_copy(update={"content_hash": source["content_hash"]})
            entered, release = asyncio.Event(), asyncio.Event()
            callbacks = []

            async def parse(*args):
                entered.set()
                await release.wait()
                return PDFManifest.model_validate(
                    {
                        "parser_version": PARSER_VERSION,
                        "pages": [
                            {
                                "page_number": 1,
                                "text": "Synthetic parsed text",
                                "extraction_method": "",
                            }
                        ],
                    }
                )

            monkeypatch.setattr("flowdesk_agent.retrieval.extract_pdf", parse)

            async def handle(request):
                if request.method == "POST":
                    callbacks.append(request.url.path)
                return httpx.Response(200, json={"data": source})

            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(handle)
            ) as http:
                index = KnowledgeIndex(pool, http)
                await index.accept(command.job_id, command.version_id)
                worker = asyncio.create_task(index.process(await index.claim()))
                await asyncio.wait_for(entered.wait(), 5)
                await cleanup_index(pool, command)
                release.set()
                await asyncio.wait_for(worker, 5)
                assert callbacks == []
                await assert_empty(pool, command)

    asyncio.run(scenario())


def test_cleanup_fences_independent_semantic_rebuild():
    async def scenario():
        async with isolated_pool() as pool:
            source, command = fixture_source()

            async def handle(request):
                return httpx.Response(200, json={"data": source})

            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(handle)
            ) as http:
                index = KnowledgeIndex(pool, http)
                await index.accept(command.job_id, command.version_id)
                await index.process(await index.claim())
                entered, release = asyncio.Event(), asyncio.Event()

                class Embedder:
                    profile, model = "synthetic-profile", "synthetic-model"

                    async def encode(self, tenant, chunks, operation, **kwargs):
                        entered.set()
                        await release.wait()
                        return [[1.0, 0.0, 0.0] for _ in chunks]

                embedder = Embedder()
                # First create real semantic rows, then remove them through the same protocol.
                release.set()
                assert await SemanticIndex(pool, embedder).rebuild(command.version_id)
                embedder.profile = "synthetic-second-profile"
                entered.clear()
                release.clear()
                worker = asyncio.create_task(
                    SemanticIndex(pool, embedder).rebuild(command.version_id)
                )
                await asyncio.wait_for(entered.wait(), 5)
                await cleanup_index(pool, command)
                release.set()
                with pytest.raises(ToolError, match="VERSION_RETIRED"):
                    await asyncio.wait_for(worker, 5)
                await assert_empty(pool, command)

    asyncio.run(scenario())
