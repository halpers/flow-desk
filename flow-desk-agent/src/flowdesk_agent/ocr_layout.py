"""Bounded Tesseract word-box reconstruction for explicit horizontal layouts."""

import csv
import io
import math
import re
import statistics
import unicodedata
from dataclasses import dataclass, replace

from flowdesk_agent.pdf_layout import MAX_LAYOUT_WIDTH, MIN_COLUMN_ROWS, LayoutError
from flowdesk_agent.pdf_limits import MAX_TEXT_BYTES, OCR_IMAGE_SIDE

MAX_TSV_BYTES = 4 * MAX_TEXT_BYTES
MAX_TSV_RECORDS = 100_000
MAX_WORDS = 20_000
BASELINE_SAMPLE_WORDS = 32
HEADER = (
    "level page_num block_num par_num line_num word_num left top width height conf text".split()
)


@dataclass(frozen=True)
class Word:
    left: int
    top: int
    width: int
    height: int
    text: str
    source_line: tuple[int, int, int]
    source_right: int | None

    @property
    def center(self):
        return self.top + self.height / 2


def units(text):
    return sum(2 if unicodedata.east_asian_width(char) in ("W", "F") else 1 for char in text)


def reconstruct(raw, layout):
    if layout not in ("columns", "table"):
        raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    if len(raw) > MAX_TSV_BYTES:
        raise LayoutError("PDF_LIMIT_EXCEEDED")
    try:
        reader = csv.reader(
            io.StringIO(raw.decode("utf-8")), delimiter="\t", quoting=csv.QUOTE_NONE
        )
        if next(reader) != HEADER:
            raise ValueError("invalid header")
        words, page_size, line_bounds = [], None, {}
        for position, row in enumerate(reader):
            if position >= MAX_TSV_RECORDS:
                raise LayoutError("PDF_LIMIT_EXCEEDED")
            if len(row) != len(HEADER):
                raise ValueError("invalid record")
            values = [int(value) for value in row[:10]]
            level, page, block, paragraph, line, word, left, top, width, height = values
            confidence = float(row[10])
            if (
                level not in range(1, 6)
                or page != 1
                or any(value < 0 for value in (block, paragraph, line, word, left, top))
                or width <= 0
                or height <= 0
                or not math.isfinite(confidence)
                or not -1 <= confidence <= 100
            ):
                raise ValueError("invalid geometry")
            if level == 1:
                if page_size is not None or left or top or max(width, height) > OCR_IMAGE_SIDE:
                    raise ValueError("invalid page")
                page_size = (width, height)
            if page_size is None or left + width > page_size[0] or top + height > page_size[1]:
                raise ValueError("out of page")
            if level == 4:
                source_line = (block, paragraph, line)
                if source_line in line_bounds:
                    raise ValueError("duplicate source line")
                line_bounds[source_line] = left + width
            if level == 5 and row[11].strip():
                text = row[11].strip()
                if any(unicodedata.category(char).startswith("C") for char in text):
                    raise ValueError("control character")
                source_line = (block, paragraph, line)
                words.append(
                    Word(left, top, width, height, text, source_line, line_bounds.get(source_line))
                )
                if len(words) > MAX_WORDS:
                    raise LayoutError("PDF_LIMIT_EXCEEDED")
        if page_size is None:
            raise ValueError("missing page")
    except (ValueError, UnicodeError, StopIteration, csv.Error):
        raise LayoutError("PDF_OCR_FAILED") from None
    if not words:
        return ""
    rows = []
    # Ignore recognizer block/line order: align geometric baselines first.
    for word in sorted(words, key=lambda item: (item.center, item.left)):
        sample = rows[-1][:BASELINE_SAMPLE_WORDS] if rows else []
        row_height = statistics.median(item.height for item in sample) if sample else word.height
        # A dash/dot has little ink height; use the surrounding text baseline.
        punctuation = all(unicodedata.category(char).startswith("P") for char in word.text)
        tolerance = max(2, (row_height if punctuation else min(word.height, row_height)) * 0.45)
        if (
            rows
            and abs(word.center - statistics.median(item.center for item in sample)) <= tolerance
        ):
            rows[-1].append(word)
        else:
            rows.append([word])
    rows = [normalize_boxes(row) for row in rows]
    words = [word for row in rows for word in row]
    origin = min(word.left for word in words)
    span = max(word.left + word.width for word in words) - origin
    cell = max(statistics.median(word.width / units(word.text) for word in words), span / 220)
    lines = []
    for row in rows:
        text, right, display_columns = "", -1, 0
        for word in sorted(row, key=lambda item: item.left):
            if word.left < right - 2:
                raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
            column = round((word.left - origin) / cell)
            padding = max(1 if text else 0, column - display_columns)
            display_columns += padding + units(word.text)
            if display_columns > MAX_LAYOUT_WIDTH:
                raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
            text += " " * padding + word.text
            right = word.left + word.width
        # Keep line geometry for column detection; Chinese word spacing is removed
        # only after ordering, so it cannot change the inferred gutter.
        lines.append(text.rstrip())
    result = column_order(rows, cell) if layout == "columns" else "\n".join(lines)
    return re.sub(r"(?<=[\u3400-\u9fff])[ \t]{1,2}(?=[\u3400-\u9fff])", "", result)


def normalize_boxes(row):
    ordered = sorted(row, key=lambda word: word.left)
    for position, (first, second) in enumerate(zip(ordered, ordered[1:])):
        if second.left >= first.left + first.width - 2:
            continue
        # Tesseract's Chinese word can carry an enclosing multi-token chunk box.
        # Repair that precise form only, preserving every recognized token.
        if (
            first.source_line == second.source_line
            and first.source_right is not None
            and first.left < second.left
            and second.left + second.width
            <= first.left + first.width + max(2, min(first.height, second.height) * 0.25)
            and re.fullmatch(r"[\u3400-\u9fff]+", first.text)
            and all(
                "\u3400" <= char <= "\u9fff" or unicodedata.category(char).startswith("P")
                for char in second.text
            )
        ):
            ordered[position] = replace(first, width=second.left - first.left)
        else:
            raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    return ordered


def column_order(rows, cell):
    # Count aligned horizontal gutters in pixels, independent of CJK display width
    # and Tesseract's block order. More than one stable gutter is ambiguous.
    changes = [0] * (OCR_IMAGE_SIDE + 2)
    for row in rows:
        ordered = sorted(row, key=lambda word: word.left)
        for first, second in zip(ordered, ordered[1:]):
            start, end = first.left + first.width, second.left
            if end - start >= 3 * cell:
                changes[start] += 1
                changes[end] -= 1
    threshold = max(MIN_COLUMN_ROWS, math.ceil(len(rows) / 2))
    regions, count = [], 0
    for position, change in enumerate(changes):
        count += change
        if count >= threshold:
            if not regions or position != regions[-1][-1] + 1:
                regions.append([])
            regions[-1].append(position)
    if len(regions) != 1 or len(regions[0]) < 3 * cell:
        raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    cut = regions[0][len(regions[0]) // 2]
    result, left, right = [], [], []

    def flush():
        result.extend(left)
        result.extend(right)
        left.clear()
        right.clear()

    for row in rows:
        ordered = sorted(row, key=lambda word: word.left)
        first = [word for word in ordered if word.left < cut]
        second = [word for word in ordered if word.left >= cut]
        small_gap = (
            first and second and second[0].left - (first[-1].left + first[-1].width) < 3 * cell
        )
        if small_gap or any(word.left < cut < word.left + word.width for word in ordered):
            flush()
            result.append(" ".join(word.text for word in ordered))
        else:
            if first:
                left.append(" ".join(word.text for word in first))
            if second:
                right.append(" ".join(word.text for word in second))
    flush()
    return "\n".join(result)
