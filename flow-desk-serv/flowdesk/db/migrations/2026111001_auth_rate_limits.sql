-- +goose Up
CREATE TABLE auth_rate_limits (
 id TEXT PRIMARY KEY,
 bucket_kind TEXT NOT NULL CHECK (bucket_kind IN ('login_global','password_global','password_account')),
 tenant_id UUID,
 user_id UUID,
 available_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY (tenant_id,user_id) REFERENCES users(tenant_id,id) ON DELETE CASCADE,
 CHECK ((bucket_kind IN ('login_global','password_global') AND tenant_id IS NULL AND user_id IS NULL AND id=bucket_kind)
     OR (bucket_kind='password_account' AND tenant_id IS NOT NULL AND user_id IS NOT NULL
         AND id=bucket_kind||':'||tenant_id::text||':'||user_id::text))
);
CREATE INDEX auth_rate_limits_reclaim ON auth_rate_limits(updated_at) WHERE bucket_kind='password_account';
-- +goose Down
-- Preserve debt across application rollback; never silently reset auth budgets.
SELECT 1;
