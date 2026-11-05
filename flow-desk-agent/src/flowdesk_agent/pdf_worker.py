"""One document per process. No HTTP, database, model or service imports."""

import io
import json
import logging
import sys
import unicodedata
from pathlib import Path

from flowdesk_agent.pdf_limits import (
    ADDRESS_SPACE_BYTES,
    CPU_SECONDS,
    LAYOUT_PARSERS,
    MAX_OCR_PAGES,
    MAX_PDF_BYTES,
    MAX_PDF_PAGES,
    MAX_TEXT_BYTES,
    MAX_TEXT_CHARACTERS,
    OCR_ADDRESS_SPACE_BYTES,
    OCR_CPU_SECONDS,
    OCR_FILE_BYTES,
    OCR_MODES,
    OCR_PARSER_VERSION,
    PARSER_VERSION,
    PDF_LAYOUTS,
)


def main():
    mode = sys.argv[1] if len(sys.argv) >= 2 else "off"
    layout = sys.argv[2] if len(sys.argv) >= 3 else "plain"
    if mode not in OCR_MODES or layout not in PDF_LAYOUTS or (layout != "plain" and mode != "off"):
        return fail("PDF_INVALID")
    if sys.platform == "linux":
        import resource

        address_space = ADDRESS_SPACE_BYTES if mode == "off" else OCR_ADDRESS_SPACE_BYTES
        cpu_seconds = CPU_SECONDS if mode == "off" else OCR_CPU_SECONDS
        resource.setrlimit(resource.RLIMIT_AS, (address_space, address_space))
        resource.setrlimit(resource.RLIMIT_CPU, (cpu_seconds, cpu_seconds))
        resource.setrlimit(resource.RLIMIT_FSIZE, (OCR_FILE_BYTES, OCR_FILE_BYTES))
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    logging.disable(logging.CRITICAL)
    from pypdf import PdfReader

    from flowdesk_agent.pdf_layout import LayoutError, extract_layout

    raw = sys.stdin.buffer.read(MAX_PDF_BYTES + 1)
    if len(raw) > MAX_PDF_BYTES:
        return fail("PDF_LIMIT_EXCEEDED")
    try:
        reader = PdfReader(io.BytesIO(raw), strict=True)
        if reader.is_encrypted:
            return fail("PDF_ENCRYPTED")
        if not 1 <= len(reader.pages) <= MAX_PDF_PAGES:
            return fail("PDF_LIMIT_EXCEEDED")
        if mode == "force" and len(reader.pages) > MAX_OCR_PAGES:
            return fail("PDF_OCR_PAGE_LIMIT")
        pages = []
        characters = 0
        byte_count = 0
        for number, page in enumerate(reader.pages, 1):
            try:
                extracted = (
                    ""
                    if mode == "force"
                    else (
                        extract_layout(page, layout) if layout != "plain" else page.extract_text()
                    )
                )
            except LayoutError as exc:
                return fail(str(exc))
            text = unicodedata.normalize("NFC", "" if mode == "force" else (extracted or ""))
            text = "".join(
                char
                for char in text
                if char in "\n\t" or not unicodedata.category(char).startswith("C")
            )
            characters += len(text) + (number > 1)
            byte_count += len(text.encode()) + (number > 1)
            if characters > MAX_TEXT_CHARACTERS or byte_count > MAX_TEXT_BYTES:
                return fail("PDF_LIMIT_EXCEEDED")
            pages.append({"page_number": number, "text": text})
        needed = [page for page in pages if mode == "force" or not page["text"].strip()]
        if mode != "off" and needed:
            if len(needed) > MAX_OCR_PAGES:
                return fail("PDF_OCR_PAGE_LIMIT")
            from flowdesk_agent.ocr_worker import RecognitionError, check_available, recognize_page

            try:
                check_available()
                Path("input.pdf").write_bytes(raw)
                for page in pages:
                    page["extraction_method"] = "text" if page["text"].strip() else "empty"
                for page in needed:
                    text = unicodedata.normalize("NFC", recognize_page(page["page_number"]))
                    text = "".join(
                        char
                        for char in text
                        if char in "\n\t" or not unicodedata.category(char).startswith("C")
                    )
                    page.update(text=text, extraction_method="ocr" if text.strip() else "empty")
                    content = "\n".join(item["text"] for item in pages)
                    if len(content) > MAX_TEXT_CHARACTERS or len(content.encode()) > MAX_TEXT_BYTES:
                        return fail("PDF_LIMIT_EXCEEDED")
            except RecognitionError as exc:
                return fail(str(exc))
        if not any(page["text"].strip() for page in pages):
            return fail("PDF_OCR_REQUIRED" if mode == "off" else "PDF_OCR_EMPTY")
        used_ocr = any(page.get("extraction_method") == "ocr" for page in pages)
        if not used_ocr:
            for page in pages:
                page.pop("extraction_method", None)
        sys.stdout.buffer.write(
            json.dumps(
                {
                    "parser_version": OCR_PARSER_VERSION
                    if used_ocr
                    else LAYOUT_PARSERS.get(layout, PARSER_VERSION),
                    "pages": pages,
                },
                ensure_ascii=False,
            ).encode()
        )
        return 0
    except MemoryError:
        return fail("PDF_LIMIT_EXCEEDED")
    except Exception:
        return fail("PDF_INVALID")


def fail(code):
    sys.stdout.buffer.write(code.encode("ascii"))
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
