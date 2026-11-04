"""Bounded text-layer PDF extraction in a child without service credentials."""

import asyncio
import os
import subprocess
import sys
from pathlib import Path

from pydantic import BaseModel, ConfigDict, Field, model_validator

from flowdesk_agent.pdf_limits import (
    MAX_PDF_BYTES,
    MAX_PDF_PAGES,
    MAX_TEXT_BYTES,
    MAX_TEXT_CHARACTERS,
    PARSE_TIMEOUT_SECONDS,
)

PDF_ERRORS = frozenset(
    {
        "PDF_INVALID",
        "PDF_ENCRYPTED",
        "PDF_OCR_REQUIRED",
        "PDF_LIMIT_EXCEEDED",
        "PDF_PARSE_TIMEOUT",
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


class PDFManifest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    parser_version: str = Field(pattern=r"^pypdf-6\.21\.4-text-v1$")
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
    )
    return environment


async def extract_pdf(raw: bytes) -> PDFManifest:
    if len(raw) > MAX_PDF_BYTES:
        raise PDFError("PDF_LIMIT_EXCEEDED")
    if not raw.startswith(b"%PDF-"):
        raise PDFError("PDF_INVALID")
    # psycopg uses a Selector loop on Windows, which has no asyncio subprocess transport.
    # Communicate in a thread; the child still has a hard timeout and is reaped on cancellation.
    process = subprocess.Popen(
        [sys.executable, "-m", "flowdesk_agent.pdf_worker"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        env=worker_environment(),
    )
    waiter = asyncio.create_task(
        asyncio.to_thread(
            process.communicate,
            raw,
            timeout=PARSE_TIMEOUT_SECONDS,
        )
    )
    try:
        output, _ = await asyncio.shield(waiter)
    except subprocess.TimeoutExpired:
        process.kill()
        await asyncio.to_thread(process.communicate)
        raise PDFError("PDF_PARSE_TIMEOUT") from None
    except asyncio.CancelledError:
        if process.poll() is None:
            process.kill()
        try:
            await waiter
        except subprocess.TimeoutExpired:
            await asyncio.to_thread(process.communicate)
        raise
    if process.returncode:
        if process.returncode < 0:
            raise PDFError("PDF_LIMIT_EXCEEDED")
        code = output.decode("ascii", errors="ignore").strip()
        raise PDFError(code)
    try:
        return PDFManifest.model_validate_json(output)
    except ValueError:
        raise PDFError("PDF_INVALID") from None
