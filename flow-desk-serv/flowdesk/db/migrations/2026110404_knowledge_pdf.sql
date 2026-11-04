-- +goose Up
ALTER TABLE document_versions
    ADD COLUMN source_format TEXT NOT NULL DEFAULT 'text' CHECK(source_format IN ('text','pdf')),
    ADD COLUMN source_bytes BYTEA NOT NULL DEFAULT '\x',
    ADD COLUMN page_texts JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(page_texts)='array'),
    ADD COLUMN parser_version TEXT NOT NULL DEFAULT '';
ALTER TABLE run_evidence ADD COLUMN page_number BIGINT NOT NULL DEFAULT 0 CHECK(page_number BETWEEN 0 AND 50);
ALTER TABLE document_versions DROP CONSTRAINT document_versions_content_check;
ALTER TABLE document_versions ADD CONSTRAINT document_versions_source_check CHECK(
    (source_format='text' AND length(content) BETWEEN 1 AND 20000 AND octet_length(source_bytes)=0)
    OR (source_format='pdf' AND length(content) BETWEEN 0 AND 100000
        AND octet_length(source_bytes) BETWEEN 1 AND 2097152)
);

-- +goose Down
-- Application rollback retains immutable source files and page provenance.
SELECT 1;
