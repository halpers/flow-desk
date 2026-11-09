# Synthetic PDF fixtures

- `policy.pdf`: three physical pages with Chinese delivery, warranty and return policies. The 36-month warranty is on page 2.
- `scan.pdf`: a raster-only page with no text layer; extraction must report `PDF_OCR_REQUIRED`.
- `encrypted.pdf`: encrypted copy of the synthetic policy; extraction must report `PDF_ENCRYPTED` without requesting a password.
- `ocr-layout-columns.pdf`: raster-only copies of the two synthetic bilingual `layout-columns.pdf` pages. Joint OCR must read each column from top to bottom before the next column; the Chinese warranty is 48 months on physical page 2.
- `ocr-layout-table.pdf`: raster-only copy of the synthetic `layout-table.pdf`. Joint OCR keeps recognized 60-month and invoice fields in the same physical row, separate from the 24-month row. Some category names are misrecognized as Latin text by the pinned recognizer; they are preserved as recognized and require manual review.
- `ocr-layout-ambiguous.pdf`: raster-only copy of synthetic three-column `layout-ambiguous.pdf`; the two-column parser must reject it instead of guessing.
- `ocr-layout-mixed.pdf`: the original first page of `layout-columns.pdf` followed by its raster-only Chinese second page, exercising physical `text`/`ocr` provenance.

Generated with ReportLab, with encryption applied using pypdf. These contain no customer information. The page-2 render and extracted Chinese text were manually checked. Tests also construct over-page-limit documents in memory. ReportLab is only a fixture-authoring tool, not an application dependency.

The four `ocr-layout-*` fixtures were authored using the pinned Linux Agent image: Poppler rendered the existing synthetic source pages as 180 DPI grayscale images, and pypdf embedded each image on a page of its original physical size. The scanned pages have no PDF text layer. Native tests use the actual pinned Poppler/Tesseract pipeline; separate synthetic TSV tests exercise geometry validation and malformed input. These controlled samples do not establish general document recognition accuracy.
