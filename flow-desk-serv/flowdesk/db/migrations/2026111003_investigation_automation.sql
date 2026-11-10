-- +goose Up
CREATE TABLE investigation_automation_policies (
 id UUID PRIMARY KEY REFERENCES tenants(id), tenant_id UUID NOT NULL UNIQUE REFERENCES tenants(id),
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0),
 idle_days BIGINT NOT NULL DEFAULT 7 CHECK(idle_days BETWEEN 1 AND 3650),
 interval_minutes BIGINT NOT NULL DEFAULT 60 CHECK(interval_minutes BETWEEN 5 AND 10080),
 owner_user_id UUID NOT NULL, owner_auth_version BIGINT NOT NULL CHECK(owner_auth_version>0),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(), cursor_id UUID,
 last_checked_at TIMESTAMPTZ, last_outcome TEXT NOT NULL DEFAULT 'never'
 CHECK(last_outcome IN ('never','returned','empty','skipped','authorization_required')),
 last_investigation_id UUID, last_revision BIGINT NOT NULL DEFAULT 0 CHECK(last_revision>=0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(id=tenant_id),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,last_investigation_id) REFERENCES source_investigations(tenant_id,id)
);
CREATE INDEX investigation_automation_due ON investigation_automation_policies(next_run_at,id) WHERE enabled;
CREATE INDEX investigation_automation_scan ON source_investigations(tenant_id,id) WHERE status='investigating';
-- +goose Down
-- Preserve confirmed authorizations, snapshots and audit evidence on application rollback.
SELECT 1;
