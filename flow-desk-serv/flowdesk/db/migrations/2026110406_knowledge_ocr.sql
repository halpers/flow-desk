-- +goose Up
ALTER TABLE document_versions ADD COLUMN ocr_mode TEXT NOT NULL DEFAULT 'off'
    CHECK(ocr_mode IN ('off','auto','force'));
ALTER TABLE document_versions ADD CONSTRAINT document_versions_ocr_source
    CHECK(source_format='pdf' OR ocr_mode='off');
ALTER TABLE run_evidence ADD COLUMN extraction_method TEXT NOT NULL DEFAULT ''
    CHECK(extraction_method IN ('','text','ocr'));

-- +goose Down
-- Preserve immutable recognition settings and citation provenance on application rollback.
SELECT 1;
