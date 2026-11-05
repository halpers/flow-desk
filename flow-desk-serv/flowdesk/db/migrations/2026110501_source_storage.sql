-- +goose Up
ALTER TABLE document_versions ADD COLUMN source_storage TEXT NOT NULL DEFAULT 'postgres'
    CHECK(source_storage IN ('postgres','s3'));
ALTER TABLE document_versions ADD COLUMN source_size BIGINT NOT NULL DEFAULT 0 CHECK(source_size>=0);
ALTER TABLE document_versions ADD COLUMN source_store_id TEXT NOT NULL DEFAULT '';
ALTER TABLE document_versions ADD COLUMN source_key TEXT NOT NULL DEFAULT '';
ALTER TABLE document_versions ADD COLUMN source_epoch BIGINT NOT NULL DEFAULT 0 CHECK(source_epoch>=0);
UPDATE document_versions SET source_size=CASE WHEN source_format='pdf' THEN octet_length(source_bytes) ELSE octet_length(content) END;
-- Older application inserts omit source_size; preserve compatibility after rollback.
-- +goose StatementBegin
CREATE FUNCTION fill_document_source_size() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.source_size=0 AND NEW.source_storage='postgres' THEN
        NEW.source_size := CASE WHEN NEW.source_format='pdf' THEN octet_length(NEW.source_bytes) ELSE octet_length(NEW.content) END;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER document_source_size BEFORE INSERT ON document_versions
    FOR EACH ROW EXECUTE FUNCTION fill_document_source_size();
ALTER TABLE document_versions DROP CONSTRAINT document_versions_source_check;
ALTER TABLE document_versions ADD CONSTRAINT document_versions_source_check CHECK(
    (source_format='text' AND length(content) BETWEEN 1 AND 20000 AND octet_length(source_bytes)=0
        AND source_storage='postgres' AND source_size=octet_length(content)) OR
    (source_format='pdf' AND length(content) BETWEEN 0 AND 100000 AND source_size BETWEEN 1 AND 2097152
        AND ((source_storage='postgres' AND octet_length(source_bytes)=source_size)
            OR (source_storage='s3' AND octet_length(source_bytes)=0))));
ALTER TABLE document_versions ADD CONSTRAINT document_versions_source_locator CHECK(
    (source_storage='postgres' AND source_store_id='' AND source_key='') OR
    (source_storage='s3' AND source_format='pdf' AND source_store_id<>'' AND source_key<>'' AND octet_length(source_bytes)=0));
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_topic_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_topic_check CHECK(topic IN ('run.execute','run.resume','run.cancel','knowledge.index','knowledge.archive'));
-- +goose Down
-- Keep immutable file locators. Restore sources before an application rollback.
SELECT 1;
