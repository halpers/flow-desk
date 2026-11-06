-- +goose Up
CREATE TABLE source_object_archives (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, version_id UUID NOT NULL,
 source_epoch BIGINT NOT NULL CHECK(source_epoch>=0), store_id TEXT NOT NULL, object_key TEXT NOT NULL,
 content_hash TEXT NOT NULL, source_size BIGINT NOT NULL CHECK(source_size BETWEEN 1 AND 2097152),
 status TEXT NOT NULL CHECK(status='retired'), created_at TIMESTAMPTZ NOT NULL,
 retired_at TIMESTAMPTZ NOT NULL, archived_by UUID NOT NULL,
 archived_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(store_id,object_key),
 FOREIGN KEY(tenant_id,version_id) REFERENCES document_versions(tenant_id,id),
 FOREIGN KEY(tenant_id,archived_by) REFERENCES users(tenant_id,id)
);
CREATE INDEX source_object_archive_version_idx ON source_object_archives(tenant_id,version_id,created_at,id);
CREATE INDEX source_cleanup_object_pending_idx ON outbox_events(tenant_id,(payload->>'object_id'))
 WHERE topic='knowledge.source_cleanup' AND status<>'delivered';
-- +goose Down
-- Archived identities and receipts require a compatible reader or consistent backup restore.
SELECT 1;
