-- +goose Up
ALTER TABLE document_versions ADD COLUMN cleanup_state TEXT NOT NULL DEFAULT 'retained'
    CHECK(cleanup_state IN ('retained','cleanup_pending','purged'));
ALTER TABLE document_versions DROP CONSTRAINT document_versions_source_check;
ALTER TABLE document_versions ADD CONSTRAINT document_versions_source_check CHECK(
    (cleanup_state='purged' AND source_storage='postgres' AND content='' AND octet_length(source_bytes)=0 AND page_texts='[]'::jsonb) OR
    (cleanup_state<>'purged' AND (
      (source_format='text' AND length(content) BETWEEN 1 AND 20000 AND octet_length(source_bytes)=0
        AND source_storage='postgres' AND source_size=octet_length(content)) OR
      (source_format='pdf' AND length(content) BETWEEN 0 AND 100000 AND source_size BETWEEN 1 AND 2097152
        AND ((source_storage='postgres' AND octet_length(source_bytes)=source_size)
          OR (source_storage='s3' AND octet_length(source_bytes)=0))))));
CREATE INDEX run_evidence_version_idx ON run_evidence(tenant_id,version_id);
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_topic_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_topic_check
    CHECK(topic IN ('run.execute','run.resume','run.cancel','knowledge.index','knowledge.archive','knowledge.cleanup'));
CREATE INDEX knowledge_outbox_version_idx ON outbox_events(tenant_id,(payload->>'version_id'),topic);
-- +goose Down
-- Purged content cannot be reconstructed by a down migration.
SELECT 1;
