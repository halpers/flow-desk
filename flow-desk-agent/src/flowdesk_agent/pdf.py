"""Bounded text-layer PDF extraction in a child without service credentials."""

import asyncio
import os
import signal
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from flowdesk_agent.pdf_limits import (
    MAX_OCR_PAGES,
    MAX_PDF_BYTES,
    MAX_PDF_PAGES,
    MAX_TEXT_BYTES,
    MAX_TEXT_CHARACTERS,
    OCR_MODES,
    OCR_PARSER_VERSION,
    OCR_TIMEOUT_SECONDS,
    PARSE_TIMEOUT_SECONDS,
    PARSER_VERSION,
    PDF_LAYOUTS,
)

PDF_ERRORS = frozenset(
    {
        "PDF_INVALID",
        "PDF_ENCRYPTED",
        "PDF_OCR_REQUIRED",
        "PDF_LIMIT_EXCEEDED",
        "PDF_PARSE_TIMEOUT",
        "PDF_OCR_UNAVAILABLE",
        "PDF_OCR_FAILED",
        "PDF_OCR_EMPTY",
        "PDF_OCR_TIMEOUT",
        "PDF_OCR_PAGE_LIMIT",
        "PDF_LAYOUT_UNSUPPORTED",
    }
)


class PDFError(Exception):
    def __init__(self, code):
        self.code = code if code in PDF_ERRORS else "PDF_INVALID"
        super().__init__(self.code)


class PDFPage(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    page_number: int = Field(ge=1, le=MAX_PDF_PAGES)
    text: str = Field(max_length=MAX_TEXT_CHARACTERS)
    extraction_method: Literal["", "text", "ocr", "empty"] = ""


class PDFManifest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    parser_version: Literal[
        PARSER_VERSION, OCR_PARSER_VERSION, "pypdf-6.21.4-columns-v1", "pypdf-6.21.4-table-v1"
    ]
    pages: list[PDFPage] = Field(min_length=1, max_length=MAX_PDF_PAGES)

    @model_validator(mode="after")
    def bounded_pages(self):
        if [page.page_number for page in self.pages] != list(range(1, len(self.pages) + 1)):
            raise ValueError("invalid physical page order")
        content = "\n".join(page.text for page in self.pages)
        if (
            not content.strip()
            or "\x00" in content
            or len(content) > MAX_TEXT_CHARACTERS
            or len(content.encode()) > MAX_TEXT_BYTES
        ):
            raise ValueError("invalid extracted text")
        if self.parser_version == OCR_PARSER_VERSION:
            ocr_count = sum(page.extraction_method == "ocr" for page in self.pages)
            attempted = sum(page.extraction_method in ("ocr", "empty") for page in self.pages)
            if not 1 <= ocr_count <= MAX_OCR_PAGES or attempted > MAX_OCR_PAGES:
                raise ValueError("invalid OCR page count")
            for page in self.pages:
                if not page.extraction_method or (
                    (page.extraction_method == "empty") != (not page.text.strip())
                ):
                    raise ValueError("invalid page provenance")
        elif any(page.extraction_method for page in self.pages):
            raise ValueError("unexpected text parser provenance")
        return self


def worker_environment():
    # Explicit allowlist: database URLs, model keys and internal capabilities never cross.
    environment = {
        key: os.environ[key]
        for key in ("SystemRoot", "SYSTEMROOT", "TEMP", "TMP")
        if key in os.environ
    }
    environment.update(
        PYTHONPATH=str(Path(__file__).resolve().parents[1]),
        PYTHONIOENCODING="utf-8",
        PYTHONUTF8="1",
        OMP_THREAD_LIMIT="1",
        OMP_NUM_THREADS="1",
        LANG="C.UTF-8",
    )
    return environment


def stop_worker(process):
    # Kill the entire Linux group, including native renderer/OCR descendants.
    try:
        if sys.platform == "linux":
            os.killpg(process.pid, signal.SIGKILL)
        elif process.poll() is None:
            process.kill()
    except ProcessLookupError:
        pass


async def extract_pdf(raw: bytes, ocr_mode: str = "off", pdf_layout: str = "plain") -> PDFManifest:
    if (
        ocr_mode not in OCR_MODES
        or pdf_layout not in PDF_LAYOUTS
        or (pdf_layout != "plain" and ocr_mode != "off")
    ):
        raise PDFError("PDF_INVALID")
    if len(raw) > MAX_PDF_BYTES:
        raise PDFError("PDF_LIMIT_EXCEEDED")
    if not raw.startswith(b"%PDF-"):
        raise PDFError("PDF_INVALID")
    # psycopg uses a Selector loop on Windows, which has no asyncio subprocess transport.
    # Communicate in a thread; the child still has a hard timeout and is reaped on cancellation.
    with tempfile.TemporaryDirectory(prefix="flowdesk-pdf-") as directory:
        return await extract_in_directory(raw, ocr_mode, directory, pdf_layout)


async def extract_in_directory(raw, ocr_mode, directory, pdf_layout="plain"):
    environment = worker_environment()
    environment["TMPDIR"] = directory
    process = subprocess.Popen(
        [sys.executable, "-m", "flowdesk_agent.pdf_worker", ocr_mode, pdf_layout],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        env=environment,
        cwd=directory,
        start_new_session=sys.platform == "linux",
    )
    waiter = asyncio.create_task(
        asyncio.to_thread(
            process.communicate,
            raw,
            timeout=PARSE_TIMEOUT_SECONDS if ocr_mode == "off" else OCR_TIMEOUT_SECONDS,
        )
    )
    try:
        output, _ = await asyncio.shield(waiter)
    except subprocess.TimeoutExpired:
        stop_worker(process)
        await asyncio.to_thread(process.communicate)
        raise PDFError("PDF_PARSE_TIMEOUT" if ocr_mode == "off" else "PDF_OCR_TIMEOUT") from None
    except asyncio.CancelledError:
        stop_worker(process)
        try:
            await waiter
        except subprocess.TimeoutExpired:
            await asyncio.to_thread(process.communicate)
        raise
    if process.returncode:
        stop_worker(process)
        if process.returncode < 0:
            raise PDFError("PDF_LIMIT_EXCEEDED")
        code = output.decode("ascii", errors="ignore").strip()
        raise PDFError(code)
    try:
        return PDFManifest.model_validate_json(output)
    except ValueError:
        raise PDFError("PDF_INVALID") from None
