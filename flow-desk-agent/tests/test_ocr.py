import asyncio
import io
import os
import subprocess
import sys
from pathlib import Path

import pytest
from pypdf import PdfReader, PdfWriter

from flowdesk_agent import pdf
from flowdesk_agent.pdf_limits import OCR_PARSER_VERSION, PARSER_VERSION

FIXTURES = Path(__file__).parent / "fixtures"
native = pytest.mark.skipif(
    os.environ.get("FLOWDESK_NATIVE_OCR_TESTS") != "1",
    reason="Real native OCR is exercised in the pinned Linux Agent image",
)


@native
def test_scan_and_mixed_pages_keep_physical_provenance():
    async def scenario():
        raw = (FIXTURES / "ocr-policy.pdf").read_bytes()
        assert all(not page.extract_text() for page in PdfReader(io.BytesIO(raw)).pages)
        with pytest.raises(pdf.PDFError, match="PDF_OCR_REQUIRED"):
            await pdf.extract_pdf(raw)
        parsed = await pdf.extract_pdf(raw, "auto")
        assert parsed.parser_version == OCR_PARSER_VERSION
        assert [page.extraction_method for page in parsed.pages] == ["ocr", "ocr"]
        assert "three business days" in parsed.pages[0].text.lower()
        assert "三十六个月" in parsed.pages[1].text
        assert "36" in parsed.pages[1].text
        mixed = await pdf.extract_pdf((FIXTURES / "ocr-mixed.pdf").read_bytes(), "auto")
        assert [page.extraction_method for page in mixed.pages] == ["text", "ocr"]
        assert "三十六个月" in mixed.pages[1].text

    asyncio.run(scenario())


@native
def test_force_recognizes_image_body_on_page_with_native_header():
    async def scenario():
        raw = (FIXTURES / "ocr-header.pdf").read_bytes()
        automatic = await pdf.extract_pdf(raw, "auto")
        assert automatic.parser_version == PARSER_VERSION
        assert "三十六个月" not in automatic.pages[0].text
        forced = await pdf.extract_pdf(raw, "force")
        assert forced.pages[0].extraction_method == "ocr"
        assert "三十六个月" in forced.pages[0].text

    asyncio.run(scenario())


@native
def test_ocr_blank_page_and_page_budget_are_explicit_failures():
    async def scenario():
        blank = PdfWriter()
        blank.add_blank_page(width=200, height=200)
        blank_output = io.BytesIO()
        blank.write(blank_output)
        with pytest.raises(pdf.PDFError, match="PDF_OCR_EMPTY"):
            await pdf.extract_pdf(blank_output.getvalue(), "auto")
        writer = PdfWriter()
        for _ in range(11):
            writer.add_blank_page(width=200, height=200)
        output = io.BytesIO()
        writer.write(output)
        for mode in ("auto", "force"):
            with pytest.raises(pdf.PDFError, match="PDF_OCR_PAGE_LIMIT"):
                await pdf.extract_pdf(output.getvalue(), mode)

    asyncio.run(scenario())


def test_ocr_manifest_rejects_forged_or_missing_provenance():
    for parser, pages in (
        (OCR_PARSER_VERSION, [{"page_number": 1, "text": "Policy"}]),
        (OCR_PARSER_VERSION, [{"page_number": 1, "text": "Policy", "extraction_method": "empty"}]),
        (PARSER_VERSION, [{"page_number": 1, "text": "Policy", "extraction_method": "ocr"}]),
        (
            OCR_PARSER_VERSION,
            [{"page_number": 1, "text": "Policy", "extraction_method": "ocr"}]
            + [
                {"page_number": number, "text": "", "extraction_method": "empty"}
                for number in range(2, 12)
            ],
        ),
    ):
        with pytest.raises(ValueError):
            pdf.PDFManifest.model_validate({"parser_version": parser, "pages": pages})
    with pytest.raises(pdf.PDFError, match="PDF_INVALID"):
        asyncio.run(pdf.extract_pdf(b"%PDF-1.7", "untrusted"))


@pytest.mark.skipif(sys.platform != "linux", reason="Linux process groups")
@pytest.mark.parametrize("cancel", [False, True])
@pytest.mark.parametrize("layout", ["plain", "columns", "table"])
def test_ocr_timeout_and_cancellation_reap_group_and_remove_directory(monkeypatch, cancel, layout):
    create = subprocess.Popen
    children, directories = [], []
    monkeypatch.setattr(pdf, "OCR_TIMEOUT_SECONDS", 0.5)
    monkeypatch.setenv("MODEL_API_KEY", "synthetic-never-inherit")

    def slow_child(*args, **kwargs):
        assert "MODEL_API_KEY" not in kwargs["env"]
        directories.append(Path(kwargs["cwd"]))
        program = (
            "import subprocess,sys,time; from pathlib import Path; "
            "p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); "
            "Path('descendant.pid').write_text(str(p.pid)); time.sleep(60)"
        )
        child = create([sys.executable, "-c", program], **kwargs)
        children.append(child)
        return child

    monkeypatch.setattr(subprocess, "Popen", slow_child)
    descendant = None

    async def scenario():
        nonlocal descendant
        task = asyncio.create_task(pdf.extract_pdf(b"%PDF-1.7", "auto", layout))
        for _ in range(100):
            if directories and (directories[0] / "descendant.pid").exists():
                descendant = int((directories[0] / "descendant.pid").read_text())
                break
            await asyncio.sleep(0.002)
        assert descendant is not None
        if cancel:
            task.cancel()
        with pytest.raises(
            asyncio.CancelledError if cancel else pdf.PDFError,
            match=None if cancel else "PDF_OCR_TIMEOUT",
        ):
            await task

    asyncio.run(scenario())
    assert children[0].poll() is not None and not directories[0].exists()
    state = Path(f"/proc/{descendant}/stat")
    # A killed descendant can briefly remain a zombie until container PID 1 reaps it.
    assert not state.exists() or state.read_text().split()[2] == "Z"
