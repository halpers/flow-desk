-- +goose Up
ALTER TABLE document_versions DROP CONSTRAINT document_versions_layout_source;
ALTER TABLE document_versions ADD CONSTRAINT document_versions_layout_source
    CHECK(pdf_layout='plain' OR source_format='pdf');
-- +goose Down
-- Keep immutable combined selections and their reviewed manifests.
SELECT 1;
