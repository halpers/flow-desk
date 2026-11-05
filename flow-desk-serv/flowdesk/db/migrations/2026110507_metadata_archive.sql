-- +goose Up
ALTER TABLE document_versions ADD COLUMN metadata_archived BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE document_versions ADD CONSTRAINT document_metadata_archive_check CHECK(
 NOT metadata_archived OR (cleanup_state='purged' AND filename='' AND parser_version=''
 AND error_code='' AND chunk_count=0 AND ocr_mode='off' AND pdf_layout='plain'));
CREATE TABLE version_metadata_archives (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, document_id UUID NOT NULL,
 filename TEXT NOT NULL, parser_version TEXT NOT NULL, error_code TEXT NOT NULL,
 chunk_count BIGINT NOT NULL CHECK(chunk_count BETWEEN 0 AND 1000),
 ocr_mode TEXT NOT NULL CHECK(ocr_mode IN ('off','auto','force')),
 pdf_layout TEXT NOT NULL CHECK(pdf_layout IN ('plain','columns','table')),
 archived_by UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,document_id,id) REFERENCES document_versions(tenant_id,document_id,id),
 FOREIGN KEY(tenant_id,archived_by) REFERENCES users(tenant_id,id)
);
CREATE INDEX document_version_hot_idx ON document_versions(tenant_id,document_id,created_at,id)
 WHERE NOT metadata_archived;
-- +goose Down
-- Restore details from a consistent backup or use the compatible application version.
-- Do not drop the archive table after metadata has been moved into it.
SELECT 1;
