-- +goose Up
ALTER TABLE document_versions ADD COLUMN pdf_layout TEXT NOT NULL DEFAULT 'plain'
    CHECK(pdf_layout IN ('plain','columns','table'));
ALTER TABLE document_versions ADD CONSTRAINT document_versions_layout_source
    CHECK(pdf_layout='plain' OR (source_format='pdf' AND ocr_mode='off'));
-- +goose Down
-- Preserve immutable extraction selection and reviewed manifests.
SELECT 1;
