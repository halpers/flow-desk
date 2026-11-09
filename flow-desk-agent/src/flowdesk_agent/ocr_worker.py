"""Fixed local OCR commands. Imported only by the disposable PDF worker."""

import re
import subprocess
import sys
from pathlib import Path

from flowdesk_agent.pdf_limits import (
    MAX_TEXT_BYTES,
    OCR_IMAGE_SIDE,
    RECOGNIZE_TIMEOUT_SECONDS,
    RENDER_TIMEOUT_SECONDS,
)

RENDERER = Path("/usr/bin/pdftoppm")
RECOGNIZER = Path("/usr/bin/tesseract")


class RecognitionError(Exception):
    pass


def check_available():
    if sys.platform != "linux" or not RENDERER.is_file() or not RECOGNIZER.is_file():
        raise RecognitionError("PDF_OCR_UNAVAILABLE")
    try:
        version = subprocess.run(
            [str(RECOGNIZER), "--version"],
            capture_output=True,
            check=True,
            timeout=2,
        ).stdout.decode("ascii", errors="ignore")
        languages = (
            subprocess.run(
                [str(RECOGNIZER), "--list-langs"],
                capture_output=True,
                check=True,
                timeout=2,
            )
            .stdout.decode("ascii", errors="ignore")
            .splitlines()
        )
        if not version.startswith("tesseract 5.5.0\n") or not {"chi_sim", "eng"} <= set(languages):
            raise RecognitionError("PDF_OCR_UNAVAILABLE")
    except (OSError, subprocess.SubprocessError):
        raise RecognitionError("PDF_OCR_UNAVAILABLE") from None


def recognize_page(number, layout="plain"):
    if layout not in ("plain", "columns", "table"):
        raise RecognitionError("PDF_LAYOUT_UNSUPPORTED")
    try:
        subprocess.run(
            [
                str(RENDERER),
                "-f",
                str(number),
                "-l",
                str(number),
                "-singlefile",
                "-gray",
                "-r",
                "300",
                "-scale-to",
                str(OCR_IMAGE_SIDE),
                "input.pdf",
                "page",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
            timeout=RENDER_TIMEOUT_SECONDS,
        )
        subprocess.run(
            [
                str(RECOGNIZER),
                "page.pgm",
                "result",
                "-l",
                "chi_sim+eng",
                "--oem",
                "1",
                "--psm",
                "3" if layout == "plain" else "11",
                "--dpi",
                "300",
                *([] if layout == "plain" else ["tsv"]),
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
            timeout=RECOGNIZE_TIMEOUT_SECONDS,
        )
        limit = MAX_TEXT_BYTES
        if layout != "plain":
            from flowdesk_agent.ocr_layout import MAX_TSV_BYTES, reconstruct

            limit = MAX_TSV_BYTES
        with Path("result.txt" if layout == "plain" else "result.tsv").open("rb") as source:
            raw = source.read(limit + 1)
        if layout != "plain":
            return reconstruct(raw, layout)
        if len(raw) > MAX_TEXT_BYTES:
            raise RecognitionError("PDF_LIMIT_EXCEEDED")
        text = raw.decode("utf-8")
        # Tesseract inserts word separators between Chinese characters. Only join
        # horizontal whitespace between CJK characters; preserve lines and numbers.
        return re.sub(r"(?<=[\u3400-\u9fff])[ \t]+(?=[\u3400-\u9fff])", "", text)
    except subprocess.TimeoutExpired:
        raise RecognitionError("PDF_OCR_TIMEOUT") from None
    except (OSError, UnicodeError, subprocess.CalledProcessError):
        raise RecognitionError("PDF_OCR_FAILED") from None
    finally:
        for filename in ("page.pgm", "result.txt", "result.tsv"):
            Path(filename).unlink(missing_ok=True)
