"""One document per process. No HTTP, database, model or service imports."""

import io
import json
import logging
import sys
import unicodedata

from flowdesk_agent.pdf_limits import (
    ADDRESS_SPACE_BYTES,
    CPU_SECONDS,
    MAX_PDF_BYTES,
    MAX_PDF_PAGES,
    MAX_TEXT_BYTES,
    MAX_TEXT_CHARACTERS,
    PARSER_VERSION,
)


def main():
    if sys.platform == "linux":
        import resource

        resource.setrlimit(resource.RLIMIT_AS, (ADDRESS_SPACE_BYTES, ADDRESS_SPACE_BYTES))
        resource.setrlimit(resource.RLIMIT_CPU, (CPU_SECONDS, CPU_SECONDS))
    logging.disable(logging.CRITICAL)
    from pypdf import PdfReader

    raw = sys.stdin.buffer.read(MAX_PDF_BYTES + 1)
    if len(raw) > MAX_PDF_BYTES:
        return fail("PDF_LIMIT_EXCEEDED")
    try:
        reader = PdfReader(io.BytesIO(raw), strict=True)
        if reader.is_encrypted:
            return fail("PDF_ENCRYPTED")
        if not 1 <= len(reader.pages) <= MAX_PDF_PAGES:
            return fail("PDF_LIMIT_EXCEEDED")
        pages = []
        characters = 0
        byte_count = 0
        for number, page in enumerate(reader.pages, 1):
            text = unicodedata.normalize("NFC", page.extract_text() or "")
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
        if not any(page["text"].strip() for page in pages):
            return fail("PDF_OCR_REQUIRED")
        sys.stdout.buffer.write(
            json.dumps(
                {
                    "parser_version": PARSER_VERSION,
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
