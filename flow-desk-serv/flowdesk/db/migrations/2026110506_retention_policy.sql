-- +goose Up
ALTER TABLE knowledge_bases ADD COLUMN retention_keep_latest BIGINT NOT NULL DEFAULT 3
 CHECK(retention_keep_latest BETWEEN 1 AND 100);
ALTER TABLE knowledge_bases ADD COLUMN retention_minimum_age_days BIGINT NOT NULL DEFAULT 30
 CHECK(retention_minimum_age_days BETWEEN 1 AND 3650);
ALTER TABLE knowledge_bases ADD COLUMN retention_revision BIGINT NOT NULL DEFAULT 1 CHECK(retention_revision>0);
-- +goose Down
-- Preserve policy revisions and audit history during application rollback.
SELECT 1;
