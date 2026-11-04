import asyncio
import base64
import hashlib
import io
import json
import os
import subprocess
import sys
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from pypdf import PdfReader, PdfWriter
from pypdf.generic import DecodedStreamObject
from test_durable_runtime import isolated_pool

from flowdesk_agent import pdf
from flowdesk_agent.retrieval import KnowledgeIndex, Retriever
from flowdesk_agent.tools.go_client import ToolError

FIXTURES = Path(__file__).parent / "fixtures"


def test_real_pdf_physical_pages_and_rejections():
    async def scenario():
        parsed = await pdf.extract_pdf((FIXTURES / "policy.pdf").read_bytes())
        assert [page.page_number for page in parsed.pages] == [1, 2, 3]
        assert "三十六个月" in parsed.pages[1].text
        assert "七天" not in parsed.pages[1].text
        for name, code in (("scan.pdf", "PDF_OCR_REQUIRED"), ("encrypted.pdf", "PDF_ENCRYPTED")):
            with pytest.raises(pdf.PDFError, match=code):
                await pdf.extract_pdf((FIXTURES / name).read_bytes())
        for raw, code in (
            (b"%PDF-1.7\ntruncated", "PDF_INVALID"),
            (b"%PDF-" + b"x" * pdf.MAX_PDF_BYTES, "PDF_LIMIT_EXCEEDED"),
        ):
            with pytest.raises(pdf.PDFError, match=code):
                await pdf.extract_pdf(raw)
        writer = PdfWriter()
        for _ in range(51):
            writer.add_blank_page(width=100, height=100)
        output = io.BytesIO()
        writer.write(output)
        with pytest.raises(pdf.PDFError, match="PDF_LIMIT_EXCEEDED"):
            await pdf.extract_pdf(output.getvalue())
        writer = PdfWriter()
        writer.add_page(PdfReader(FIXTURES / "policy.pdf").pages[0])
        stream = DecodedStreamObject()
        stream.set_data(b"BT /F1 12 Tf (" + b"x" * 100_001 + b") Tj ET")
        writer.pages[0].replace_contents(stream)
        output = io.BytesIO()
        writer.write(output)
        with pytest.raises(pdf.PDFError, match="PDF_LIMIT_EXCEEDED"):
            await pdf.extract_pdf(output.getvalue())

    asyncio.run(scenario())


def test_parser_deadline_kills_child_and_excludes_service_credentials(monkeypatch):
    create = subprocess.Popen
    children = []
    monkeypatch.setenv("MODEL_API_KEY", "synthetic-secret-must-not-enter-parser")
    monkeypatch.setenv("DATABASE_URL", "synthetic-private-database")
    monkeypatch.setattr(pdf, "PARSE_TIMEOUT_SECONDS", 0.1)

    def slow_child(*args, **kwargs):
        assert "MODEL_API_KEY" not in kwargs["env"]
        assert "DATABASE_URL" not in kwargs["env"]
        child = create([sys.executable, "-c", "import time; time.sleep(30)"], **kwargs)
        children.append(child)
        return child

    monkeypatch.setattr(subprocess, "Popen", slow_child)
    with pytest.raises(pdf.PDFError, match="PDF_PARSE_TIMEOUT"):
        asyncio.run(pdf.extract_pdf(b"%PDF-1.7"))
    assert len(children) == 1 and children[0].returncode is not None
    monkeypatch.setattr(pdf, "PARSE_TIMEOUT_SECONDS", 8)

    async def cancelled():
        task = asyncio.create_task(pdf.extract_pdf(b"%PDF-1.7"))
        while len(children) < 2:
            await asyncio.sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert children[1].poll() is not None

    asyncio.run(cancelled())


@pytest.mark.parametrize(
    "filename,error_code,ocr_mode",
    [
        ("policy.pdf", "", "off"),
        ("scan.pdf", "PDF_OCR_REQUIRED", "off"),
        pytest.param(
            "ocr-policy.pdf",
            "",
            "auto",
            marks=pytest.mark.skipif(
                os.environ.get("FLOWDESK_NATIVE_OCR_TESTS") != "1",
                reason="Pinned native OCR image",
            ),
        ),
        ("scan.pdf", "PDF_OCR_PAGE_LIMIT", "force_limit"),
    ],
)
def test_pdf_index_callback_crash_replays_manifest_or_durable_error(
    monkeypatch, filename, error_code, ocr_mode
):
    async def scenario():
        async with isolated_pool() as pool:
            tenant, version, job_id = uuid4(), uuid4(), uuid4()
            raw = (FIXTURES / filename).read_bytes()
            if ocr_mode == "force_limit":
                writer = PdfWriter()
                for _ in range(11):
                    writer.add_blank_page(width=200, height=200)
                output = io.BytesIO()
                writer.write(output)
                raw = output.getvalue()
                ocr_selection = "force"
            else:
                ocr_selection = ocr_mode
            source = {
                "ocr_mode": ocr_selection,
                "source_format": "pdf",
                "content": "",
                "source_base64": base64.b64encode(raw).decode(),
                "content_hash": hashlib.sha256(raw).hexdigest(),
                "tenant_id": str(tenant),
                "document_id": str(uuid4()),
                "knowledge_base_id": str(uuid4()),
            }
            callbacks = []

            async def handle(request):
                if request.method == "GET":
                    return httpx.Response(200, json={"data": source})
                callbacks.append(json.loads(request.content))
                return httpx.Response(503 if len(callbacks) == 1 else 200)

            async with httpx.AsyncClient(
                base_url="https://go.test", transport=httpx.MockTransport(handle)
            ) as http:
                index = KnowledgeIndex(pool, http)
                await index.accept(job_id, version)
                old = await index.claim()
                with pytest.raises(ToolError, match="INDEX_CALLBACK_UNAVAILABLE"):
                    await index.process(old)
                async with pool.connection() as conn:
                    await conn.execute(
                        "UPDATE knowledge_jobs SET lease_until=now()-interval '1 second' "
                        "WHERE job_id=%s",
                        (job_id,),
                    )
                # A restarted owner reuses durable results and fences the old owner.
                fresh_index = KnowledgeIndex(pool, http)
                fresh = await fresh_index.claim()
                assert fresh["attempts"] == old["attempts"] + 1

                async def no_reparse(raw):
                    raise AssertionError("persisted extraction/error must be reused")

                monkeypatch.setattr("flowdesk_agent.retrieval.extract_pdf", no_reparse)
                await index.process(old)
                assert len(callbacks) == 1
                await fresh_index.process(fresh)
                assert callbacks[0] == callbacks[1]
                assert callbacks[1]["error_code"] == error_code
                async with pool.connection() as conn:
                    rows = await (
                        await conn.execute("SELECT * FROM knowledge_chunks ORDER BY position")
                    ).fetchall()
                    state = await (
                        await conn.execute(
                            "SELECT status,error_code FROM knowledge_jobs WHERE job_id=%s",
                            (job_id,),
                        )
                    ).fetchone()
                assert state["status"] == "completed" and state["error_code"] == error_code
                if error_code:
                    assert not rows and callbacks[1]["status"] == "failed"
                else:
                    assert [row["page_number"] for row in rows] == (
                        [1, 2] if filename == "ocr-policy.pdf" else [1, 2, 3]
                    )
                    assert callbacks[1]["page_texts"][1]["page_number"] == 2
                    if filename == "ocr-policy.pdf":
                        assert callbacks[1]["page_texts"][1]["extraction_method"] == "ocr"
                    retriever = Retriever(pool, None, profile="baseline")
                    chosen = await retriever.rank("三十六个月", tenant, [version], mode="lexical")
                    assert chosen[0]["page_number"] == 2
                    assert (
                        await retriever.rank("三十六个月", uuid4(), [version], mode="lexical") == []
                    )

    asyncio.run(scenario())
