"""Conservative, explicit text-layer layout profiles; imported by the isolated worker only."""

import math

from pypdf import mult

MAX_LAYOUT_WIDTH = 240
MAX_LAYOUT_FRAGMENTS = 20_000
MIN_COLUMN_ROWS = 3
MIN_GUTTER_WIDTH = 3
SECTION_BLANK_ROWS = 4


class LayoutError(Exception):
    pass


def extract_layout(page, mode):
    if page.rotation % 360:
        raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    fragments = 0

    def horizontal_text(text, current_matrix, text_matrix, font, size):
        nonlocal fragments
        if not text.strip():
            return
        fragments += 1
        matrix = mult(text_matrix, current_matrix)
        if fragments > MAX_LAYOUT_FRAGMENTS:
            raise LayoutError("PDF_LIMIT_EXCEEDED")
        if (
            not all(math.isfinite(value) for value in matrix)
            or abs(matrix[1]) > 0.001
            or abs(matrix[2]) > 0.001
            or matrix[0] <= 0
            or matrix[3] <= 0
        ):
            raise LayoutError("PDF_LAYOUT_UNSUPPORTED")

    page.extract_text(visitor_text=horizontal_text)
    text = page.extract_text(
        extraction_mode="layout", layout_mode_space_vertically=True, layout_mode_strip_rotated=False
    )
    lines = [line.rstrip() for line in text.splitlines()]
    if any(len(line) > MAX_LAYOUT_WIDTH for line in lines):
        raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    if mode == "table":
        return "\n".join(line for line in lines if line.strip())
    return read_columns(lines)


def read_columns(lines):
    occupied = [(number, line) for number, line in enumerate(lines) if line.strip()]
    if not occupied:
        return ""
    width = max(len(line) for _, line in occupied)
    threshold = max(MIN_COLUMN_ROWS, math.ceil(len(occupied) / 2))

    def paired(line, cut):
        return (
            line[:cut].strip()
            and line[cut : cut + MIN_GUTTER_WIDTH].isspace()
            and line[cut + MIN_GUTTER_WIDTH :].strip()
        )

    candidates = [
        cut
        for cut in range(1, width - MIN_GUTTER_WIDTH)
        if sum(bool(paired(line, cut)) for _, line in occupied) >= threshold
    ]
    regions = []
    for cut in candidates:
        if not regions or cut != regions[-1][-1] + 1:
            regions.append([])
        regions[-1].append(cut)
    if len(regions) != 1:
        raise LayoutError("PDF_LAYOUT_UNSUPPORTED")
    cut = regions[0][len(regions[0]) // 2]
    body_rows = [number for number, line in occupied if paired(line, cut)]
    first, last = body_rows[0], body_rows[-1]
    # Include shorter/unbalanced column continuations, stopping at major vertical gaps.
    for direction in (-1, 1):
        boundary = first if direction < 0 else last
        blanks = 0
        cursor = boundary + direction
        while 0 <= cursor < len(lines):
            if not lines[cursor].strip():
                blanks += 1
                if blanks >= SECTION_BLANK_ROWS:
                    break
            else:
                blanks = 0
                boundary = cursor
            cursor += direction
        if direction < 0:
            first = boundary
        else:
            last = boundary
    result = [line.strip() for line in lines[:first] if line.strip()]
    left, right = [], []

    def flush():
        result.extend(left)
        result.extend(right)
        left.clear()
        right.clear()

    for line in lines[first : last + 1]:
        if not line.strip():
            continue
        gutter = line[cut : cut + MIN_GUTTER_WIDTH]
        if gutter.strip():
            flush()
            result.append(line.strip())
            continue
        if line[:cut].strip():
            left.append(line[:cut].strip())
        if line[cut + MIN_GUTTER_WIDTH :].strip():
            right.append(line[cut + MIN_GUTTER_WIDTH :].strip())
    flush()
    result.extend(line.strip() for line in lines[last + 1 :] if line.strip())
    return "\n".join(result)
