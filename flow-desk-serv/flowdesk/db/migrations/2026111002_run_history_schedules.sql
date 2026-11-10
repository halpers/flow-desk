-- +goose Up
ALTER TABLE run_history_operations ADD CONSTRAINT run_history_schedule_binding UNIQUE (tenant_id,run_id,id);
ALTER TABLE audit_events DROP CONSTRAINT audit_events_source_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_source_check CHECK (source IN ('agent','manual','user','scheduled'));
CREATE TABLE run_history_schedules (
    id UUID PRIMARY KEY REFERENCES tenants(id),
    tenant_id UUID NOT NULL UNIQUE REFERENCES tenants(id),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    minimum_age_days BIGINT NOT NULL DEFAULT 30 CHECK (minimum_age_days BETWEEN 30 AND 3650),
    interval_minutes BIGINT NOT NULL DEFAULT 60 CHECK (interval_minutes BETWEEN 5 AND 10080),
    owner_user_id UUID NOT NULL,
    owner_auth_version BIGINT NOT NULL CHECK (owner_auth_version > 0),
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    cursor_run_id UUID,
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    last_checked_at TIMESTAMPTZ,
    last_outcome TEXT NOT NULL DEFAULT 'never' CHECK (last_outcome IN ('never','completed','skipped','empty','pending','unavailable','authorization_required')),
    last_run_id UUID,
    last_operation_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = tenant_id),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (last_operation_id IS NULL OR last_run_id IS NOT NULL),
    FOREIGN KEY (tenant_id,owner_user_id) REFERENCES users(tenant_id,id),
    FOREIGN KEY (tenant_id,last_run_id) REFERENCES runs(tenant_id,id),
    FOREIGN KEY (tenant_id,last_run_id,last_operation_id) REFERENCES run_history_operations(tenant_id,run_id,id)
);
CREATE INDEX run_history_schedules_due ON run_history_schedules(next_run_at,id) WHERE enabled;
CREATE INDEX runs_history_schedule_scan ON runs(tenant_id,id) WHERE status IN ('succeeded','failed','expired','cancelled');

-- +goose Down
-- Preserve explicit authorizations and pending operation bindings during rollback.
SELECT 1;
