-- +goose Up
CREATE TABLE source_objects (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, version_id UUID NOT NULL,
 source_epoch BIGINT NOT NULL CHECK(source_epoch>=0), store_id TEXT NOT NULL, object_key TEXT NOT NULL,
 content_hash TEXT NOT NULL, source_size BIGINT NOT NULL CHECK(source_size BETWEEN 1 AND 2097152),
 status TEXT NOT NULL DEFAULT 'registered' CHECK(status IN ('registered','cleanup_pending','retired')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), retired_at TIMESTAMPTZ,
 UNIQUE(store_id,object_key), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,version_id) REFERENCES document_versions(tenant_id,id)
);
INSERT INTO source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size)
 SELECT id,tenant_id,id,source_epoch,source_store_id,source_key,content_hash,source_size
 FROM document_versions WHERE source_storage='s3';
CREATE INDEX source_object_version_idx ON source_objects(tenant_id,version_id,created_at,id);
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_topic_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_topic_check
 CHECK(topic IN ('run.execute','run.resume','run.cancel','knowledge.index','knowledge.archive','knowledge.cleanup','knowledge.source_cleanup'));
-- +goose Down
-- Object tombstones and their ledger cannot be reconstructed by rollback.
SELECT 1;
