-- +goose Up
CREATE TABLE source_investigations (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id),
 object_ref TEXT NOT NULL CHECK(object_ref ~ '^[0-9a-f]{64}$'),
 store_id TEXT NOT NULL, object_key TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('open','investigating','closed')),
 note TEXT NOT NULL CHECK(length(note) BETWEEN 1 AND 2000),
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 created_by UUID NOT NULL, updated_by UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,object_ref),
 FOREIGN KEY(tenant_id,created_by) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,updated_by) REFERENCES users(tenant_id,id)
);
-- +goose Down
-- Investigation history, immutable receipts and audit require consistent backups.
SELECT 1;
