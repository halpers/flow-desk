import asyncio
import io
import os
from collections import Counter
from pathlib import Path

import pytest
from pypdf import PdfReader, PdfWriter

from flowdesk_agent import pdf
from flowdesk_agent.ocr_layout import HEADER, MAX_TSV_BYTES, reconstruct
from flowdesk_agent.pdf_layout import LayoutError
from flowdesk_agent.pdf_limits import OCR_COLUMNS_PARSER_VERSION, OCR_TABLE_PARSER_VERSION

FIXTURES = Path(__file__).parent / "fixtures"
native = pytest.mark.skipif(
    os.environ.get("FLOWDESK_NATIVE_OCR_TESTS") != "1",
    reason="Real native OCR requires the pinned Linux image",
)


def tsv(words):
    records = [HEADER, [1, 1, 0, 0, 0, 0, 0, 0, 1000, 1000, -1, ""]]
    records.extend([5, 1, 1, 1, 1, 1, x, y, width, 16, 90, text] for x, y, width, text in words)
    return ("\n".join("\t".join(map(str, row)) for row in records) + "\n").encode()


def test_word_geometry_orders_columns_without_losing_or_inventing_words():
    words = []
    for index in range(6):
        words += [
            (20, 100 + index * 25, 140, f"LEFT-{index}"),
            (450, 100 + index * 25, 140, f"RIGHT-{index}"),
        ]
    # Input records deliberately come in reverse recognizer order.
    result = reconstruct(tsv(reversed(words)), "columns")
    assert result.splitlines() == [f"LEFT-{i}" for i in range(6)] + [f"RIGHT-{i}" for i in range(6)]
    assert Counter(result.split()) == Counter(word[3] for word in words)


def test_table_rows_keep_chinese_terms_numbers_and_cell_separation():
    words = [
        (20, 100, 120, "专业设备"),
        (250, 101, 140, "六十个月"),
        (550, 100, 200, "设备编号与发票"),
        (20, 160, 120, "基础设备"),
        (250, 160, 100, "24个月"),
    ]
    rows = reconstruct(tsv(reversed(words)), "table").splitlines()
    assert len(rows) == 2
    assert all(term in rows[0] for term in ("专业设备", "六十个月", "设备编号与发票"))
    assert "24个月" not in rows[0] and "24个月" in rows[1]
    assert "   " in rows[0]


def test_full_width_header_footer_and_small_punctuation_keep_their_physical_rows():
    words = [(220, 20, 200, "HEADER")]
    for index in range(6):
        words += [
            (20, 100 + index * 25, 140, f"LEFT-{index}"),
            (450, 100 + index * 25, 140, f"RIGHT-{index}"),
        ]
    words += [(280, 300, 20, "end"), (310, 300, 20, "note")]
    result = reconstruct(tsv(words), "columns").splitlines()
    assert result[0] == "HEADER" and result[-1] == "end note"
    assert result.index("LEFT-5") < result.index("RIGHT-0")
    raw = tsv([(20, 100, 100, "first"), (150, 100, 100, "last")])
    raw += b"5\t1\t1\t1\t1\t1\t130\t107\t8\t2\t90\t-\n"
    assert reconstruct(raw, "table").splitlines()[0].split() == ["first", "-", "last"]
    quoted = tsv([(20, 100, 20, '"'), (60, 100, 100, "policy")])
    assert reconstruct(quoted, "table").split() == ['"', "policy"]


def test_chinese_enclosing_line_box_preserves_tokens_but_cross_line_overlap_fails():
    raw = tsv([(20, 100, 160, "二"), (60, 100, 30, "十"), (100, 100, 30, "四")])
    source_line = b"4\t1\t1\t1\t1\t0\t20\t100\t160\t16\t-1\t\n"
    rows = raw.splitlines(keepends=True)
    raw = b"".join(rows[:2]) + source_line + b"".join(rows[2:])
    assert reconstruct(raw, "table").replace(" ", "") == "二十四"
    different_line = raw.replace(b"5\t1\t1\t1\t1\t1\t60", b"5\t1\t2\t1\t1\t1\t60")
    with pytest.raises(LayoutError, match="PDF_LAYOUT_UNSUPPORTED"):
        reconstruct(different_line, "table")
    crossing = tsv([(20, 100, 80, "二"), (60, 100, 80, "十")])
    rows = crossing.splitlines(keepends=True)
    crossing = b"".join(rows[:2]) + source_line + b"".join(rows[2:])
    with pytest.raises(LayoutError, match="PDF_LAYOUT_UNSUPPORTED"):
        reconstruct(crossing, "table")
    words = [(20, 100, 117, "七"), (78, 100, 34, "天"), (111, 100, 38, "内")]
    raw = tsv(words + [(200, 100, 80, "申请"), (265, 100, 17, "。")])
    rows = raw.splitlines(keepends=True)
    source_line = b"4\t1\t1\t1\t1\t0\t20\t100\t300\t16\t-1\t\n"
    raw = b"".join(rows[:2]) + source_line + b"".join(rows[2:])
    assert reconstruct(raw, "table").replace(" ", "") == "七天内申请。"


@pytest.mark.parametrize("columns", [1, 3])
def test_ambiguous_or_missing_gutter_fails_without_plain_fallback(columns):
    words = [
        (20 + column * 300, 50 + row * 30, 100, f"C{column}-{row}")
        for row in range(6)
        for column in range(columns)
    ]
    with pytest.raises(LayoutError, match="PDF_LAYOUT_UNSUPPORTED"):
        reconstruct(tsv(words), "columns")


@pytest.mark.parametrize("mutation", ["outside", "nan", "unknown_header", "control", "negative"])
def test_untrusted_tsv_geometry_and_records_are_rejected(mutation):
    raw = tsv([(20, 100, 140, "synthetic")])
    if mutation == "outside":
        raw = raw.replace(b"20\t100\t140", b"990\t100\t140")
    elif mutation == "negative":
        raw = raw.replace(b"20\t100\t140", b"-1\t100\t140")
    elif mutation == "nan":
        raw = raw.replace(b"\t90\t", b"\tNaN\t")
    elif mutation == "unknown_header":
        raw = raw.replace(b"level", b"level_extra", 1)
    else:
        raw = raw.replace(b"synthetic", b"secret\x00text")
    with pytest.raises(LayoutError, match="PDF_OCR_FAILED"):
        reconstruct(raw, "table")


def test_limits_and_overlapping_boxes_fail_explicitly(monkeypatch):
    with pytest.raises(LayoutError, match="PDF_LIMIT_EXCEEDED"):
        reconstruct(b" " * (MAX_TSV_BYTES + 1), "table")
    raw = tsv([(20, 100, 140, "first"), (25, 100, 140, "overlap")])
    with pytest.raises(LayoutError, match="PDF_LAYOUT_UNSUPPORTED"):
        reconstruct(raw, "table")
    with pytest.raises(LayoutError, match="PDF_LAYOUT_UNSUPPORTED"):
        reconstruct(tsv([(20, 100, 800, "x" * 241)]), "table")
    with monkeypatch.context() as context:
        context.setattr("flowdesk_agent.ocr_layout.MAX_TSV_RECORDS", 1)
        with pytest.raises(LayoutError, match="PDF_LIMIT_EXCEEDED"):
            reconstruct(raw, "table")
    monkeypatch.setattr("flowdesk_agent.ocr_layout.MAX_WORDS", 1)
    with pytest.raises(LayoutError, match="PDF_LIMIT_EXCEEDED"):
        reconstruct(raw, "table")


@native
def test_real_scanned_bilingual_columns_and_mixed_provenance():
    async def scenario():
        raw = (FIXTURES / "ocr-layout-columns.pdf").read_bytes()
        assert all(not page.extract_text() for page in PdfReader(io.BytesIO(raw)).pages)
        with pytest.raises(pdf.PDFError, match="PDF_OCR_REQUIRED"):
            await pdf.extract_pdf(raw, "off", "columns")
        parsed = await pdf.extract_pdf(raw, "auto", "columns")
        assert parsed.parser_version == OCR_COLUMNS_PARSER_VERSION
        assert [page.extraction_method for page in parsed.pages] == ["ocr", "ocr"]
        assert parsed.pages[0].text.index("LEFT-5") < parsed.pages[0].text.index("RIGHT-1")
        assert "四十八个月" in parsed.pages[1].text
        mixed = await pdf.extract_pdf(
            (FIXTURES / "ocr-layout-mixed.pdf").read_bytes(), "auto", "columns"
        )
        assert mixed.parser_version == OCR_COLUMNS_PARSER_VERSION
        assert [page.extraction_method for page in mixed.pages] == ["text", "ocr"]
        assert [page.page_number for page in mixed.pages] == [1, 2]
        # Force must recognize the native page too, rather than silently bypass OCR.
        forced = await pdf.extract_pdf(
            (FIXTURES / "layout-columns.pdf").read_bytes(), "force", "columns"
        )
        assert all(page.extraction_method == "ocr" for page in forced.pages)
        assert forced.pages[0].text.index("LEFT-5") < forced.pages[0].text.index("RIGHT-1")
        assert "四十八个月" in forced.pages[1].text

    asyncio.run(scenario())


@native
def test_real_scanned_table_preserves_row_association_and_rejects_three_columns():
    async def scenario():
        parsed = await pdf.extract_pdf(
            (FIXTURES / "ocr-layout-table.pdf").read_bytes(), "force", "table"
        )
        assert parsed.parser_version == OCR_TABLE_PARSER_VERSION
        assert parsed.pages[0].extraction_method == "ocr"
        row = next(line for line in parsed.pages[0].text.splitlines() if "六十个月" in line)
        assert "设备编号与发票" in row
        assert "二十四个月" not in row
        with pytest.raises(pdf.PDFError, match="PDF_LAYOUT_UNSUPPORTED"):
            await pdf.extract_pdf(
                (FIXTURES / "ocr-layout-ambiguous.pdf").read_bytes(), "auto", "columns"
            )
        writer = PdfWriter()
        writer.add_page(PdfReader(FIXTURES / "ocr-layout-columns.pdf").pages[0].rotate(90))
        output = io.BytesIO()
        writer.write(output)
        with pytest.raises(pdf.PDFError, match="PDF_LAYOUT_UNSUPPORTED"):
            await pdf.extract_pdf(output.getvalue(), "force", "columns")

    asyncio.run(scenario())


def test_new_parsers_require_complete_ocr_provenance():
    for parser in (OCR_COLUMNS_PARSER_VERSION, OCR_TABLE_PARSER_VERSION):
        for method in ("", "text", "empty"):
            with pytest.raises(ValueError):
                pdf.PDFManifest.model_validate(
                    {
                        "parser_version": parser,
                        "pages": [
                            {"page_number": 1, "text": "synthetic", "extraction_method": method}
                        ],
                    }
                )
