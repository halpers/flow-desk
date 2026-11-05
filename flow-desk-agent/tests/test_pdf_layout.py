import asyncio
import io
from collections import Counter
from pathlib import Path

import pytest
from pypdf import PdfReader, PdfWriter
from pypdf.generic import DecodedStreamObject

from flowdesk_agent.pdf import PDFError, extract_pdf

FIXTURES = Path(__file__).parent / "fixtures"


def test_real_bilingual_columns_preserve_content_and_physical_pages():
    async def scenario():
        raw = (FIXTURES / "layout-columns.pdf").read_bytes()
        plain = await extract_pdf(raw)
        columns = await extract_pdf(raw, pdf_layout="columns")
        assert columns.parser_version == "pypdf-6.21.4-columns-v1"
        assert [page.page_number for page in columns.pages] == [1, 2]
        for old, new in zip(plain.pages, columns.pages, strict=True):
            # No content may disappear or be invented while changing reading order.
            assert Counter(filter(lambda char: not char.isspace(), old.text)) == Counter(
                filter(lambda char: not char.isspace(), new.text)
            )
        assert plain.pages[0].text.index("RIGHT-1") < plain.pages[0].text.index("LEFT-5")
        assert columns.pages[0].text.index("LEFT-5") < columns.pages[0].text.index("RIGHT-1")
        assert columns.pages[1].text.index("左栏五") < columns.pages[1].text.index("右栏一")
        assert "四十八个月" in columns.pages[1].text
        assert columns.pages[1].text.startswith("合成双栏售后政策")
        assert columns.pages[1].text.endswith("Synthetic fixture - no customer data")

    asyncio.run(scenario())


def test_real_table_keeps_row_association_and_no_content_loss():
    async def scenario():
        raw = (FIXTURES / "layout-table.pdf").read_bytes()
        plain = await extract_pdf(raw)
        table = await extract_pdf(raw, pdf_layout="table")
        assert table.parser_version == "pypdf-6.21.4-table-v1"
        old, new = plain.pages[0].text, table.pages[0].text
        assert Counter("".join(old.split())) == Counter("".join(new.split()))
        assert not any("天琴专业设备" in line and "六十个月" in line for line in old.splitlines())
        row = next(line for line in new.splitlines() if "天琴专业设备" in line)
        assert "六十个月" in row and "设备编号与发票" in row
        assert "二十四个月" not in row

    asyncio.run(scenario())


@pytest.mark.parametrize(
    "filename,mode",
    [
        ("layout-ambiguous.pdf", "columns"),
        ("layout-rotated.pdf", "table"),
        ("layout-rotated.pdf", "columns"),
        ("policy.pdf", "columns"),
    ],
)
def test_unsupported_layout_fails_without_plain_fallback(filename, mode):
    with pytest.raises(PDFError, match="PDF_LAYOUT_UNSUPPORTED"):
        asyncio.run(extract_pdf((FIXTURES / filename).read_bytes(), pdf_layout=mode))


def test_layout_rejects_invalid_combinations_and_unbounded_width():
    raw = (FIXTURES / "layout-columns.pdf").read_bytes()
    for ocr, layout in [("off", "guess"), ("auto", "columns"), ("force", "table")]:
        with pytest.raises(PDFError, match="PDF_INVALID"):
            asyncio.run(extract_pdf(raw, ocr, layout))
    writer = PdfWriter()
    writer.add_page(PdfReader(FIXTURES / "layout-columns.pdf").pages[0])
    stream = DecodedStreamObject()
    stream.set_data(b"BT /F1 12 Tf 1 0 0 1 40 700 Tm (" + b"x" * 300 + b") Tj ET")
    writer.pages[0].replace_contents(stream)
    output = io.BytesIO()
    writer.write(output)
    with pytest.raises(PDFError, match="PDF_LAYOUT_UNSUPPORTED"):
        asyncio.run(extract_pdf(output.getvalue(), pdf_layout="table"))
