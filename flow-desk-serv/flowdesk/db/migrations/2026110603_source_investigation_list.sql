-- +goose Up
CREATE INDEX source_investigations_tenant_created_idx ON source_investigations(tenant_id,created_at DESC,id DESC);
CREATE INDEX source_investigations_tenant_status_created_idx ON source_investigations(tenant_id,status,created_at DESC,id DESC);
-- +goose Down
-- Keep additive indexes and the migration ledger during application rollback.
SELECT 1;
