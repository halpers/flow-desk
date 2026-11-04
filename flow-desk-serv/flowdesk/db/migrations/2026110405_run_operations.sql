-- +goose Up
ALTER TABLE runs ADD COLUMN retry_of_run_id UUID,
    ADD COLUMN retry_depth BIGINT NOT NULL DEFAULT 0 CHECK(retry_depth BETWEEN 0 AND 3),
    ADD CONSTRAINT runs_retry_tenant_fk FOREIGN KEY(tenant_id,retry_of_run_id) REFERENCES runs(tenant_id,id);
CREATE UNIQUE INDEX runs_one_retry ON runs(tenant_id,retry_of_run_id) WHERE retry_of_run_id IS NOT NULL;
CREATE INDEX runs_operations_own ON runs(tenant_id,actor_user_id,created_at DESC,id DESC);
CREATE INDEX runs_operations_tenant ON runs(tenant_id,created_at DESC,id DESC);

-- +goose Down
-- Keep retry provenance on application rollback.
SELECT 1;
