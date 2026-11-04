# Synthetic PDF fixtures

- `policy.pdf`: three physical pages with Chinese delivery, warranty and return policies. The 36-month warranty is on page 2.
- `scan.pdf`: a raster-only page with no text layer; extraction must report `PDF_OCR_REQUIRED`.
- `encrypted.pdf`: encrypted copy of the synthetic policy; extraction must report `PDF_ENCRYPTED` without requesting a password.

Generated with ReportLab, with encryption applied using pypdf. These contain no customer information. The page-2 render and extracted Chinese text were manually checked. Tests also construct over-page-limit documents in memory. ReportLab is only a fixture-authoring tool, not an application dependency.
