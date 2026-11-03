-- +goose Up
ALTER TABLE runs ADD COLUMN wait_expires_at TIMESTAMPTZ;
ALTER TABLE runs ADD COLUMN resume_data JSONB NOT NULL DEFAULT '{}';
ALTER TABLE runs ADD COLUMN input_count BIGINT NOT NULL DEFAULT 0 CHECK (input_count BETWEEN 0 AND 3);
ALTER TABLE messages DROP CONSTRAINT messages_tenant_id_run_id_role_key;
CREATE UNIQUE INDEX messages_one_assistant_idx ON messages(tenant_id,run_id) WHERE role='assistant';
CREATE TABLE pending_inputs (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, run_id UUID NOT NULL,
 question TEXT NOT NULL CHECK(length(question) BETWEEN 1 AND 1000),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','answered','expired','cancelled')),
 answer TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1,
 expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id)
);
CREATE UNIQUE INDEX pending_inputs_active_idx ON pending_inputs(tenant_id,run_id) WHERE status='pending';
CREATE TABLE actions (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, run_id UUID NOT NULL,
 tool_name TEXT NOT NULL CHECK(tool_name IN ('ticket.create','ticket.add_note')),
 tool_version TEXT NOT NULL DEFAULT '1', arguments JSONB NOT NULL,
 request_hash TEXT NOT NULL, payload_digest TEXT NOT NULL,
 resource_versions JSONB NOT NULL, evidence_versions JSONB NOT NULL,
 status TEXT NOT NULL DEFAULT 'prepared' CHECK(status IN ('prepared','executed','invalidated','cancelled')),
 expires_at TIMESTAMPTZ NOT NULL, version BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id)
);
CREATE TABLE approvals (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, run_id UUID NOT NULL, action_id UUID NOT NULL,
 requester_user_id UUID NOT NULL, status TEXT NOT NULL DEFAULT 'prepared'
 CHECK(status IN ('prepared','pending','approved','rejected','expired','invalidated','cancelled')),
 payload_digest TEXT NOT NULL, display_snapshot JSONB NOT NULL,
 decided_by UUID, decision_reason TEXT NOT NULL DEFAULT '', decided_at TIMESTAMPTZ,
 expires_at TIMESTAMPTZ NOT NULL, version BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,action_id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id),
 FOREIGN KEY(tenant_id,action_id) REFERENCES actions(tenant_id,id),
 FOREIGN KEY(tenant_id,requester_user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,decided_by) REFERENCES users(tenant_id,id)
);
CREATE TABLE tickets (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, ticket_no TEXT NOT NULL,
 order_id UUID NOT NULL, title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 120),
 description TEXT NOT NULL CHECK(length(description) BETWEEN 1 AND 4000),
 category TEXT NOT NULL CHECK(category IN ('after_sales','logistics','other')),
 priority TEXT NOT NULL CHECK(priority IN ('normal','urgent')),
 status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','in_progress','resolved','closed')),
 assignee_user_id UUID, created_by UUID NOT NULL, source_action_id UUID,
 version BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,ticket_no), UNIQUE(tenant_id,source_action_id),
 FOREIGN KEY(tenant_id,order_id) REFERENCES orders(tenant_id,id),
 FOREIGN KEY(tenant_id,created_by) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,assignee_user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,source_action_id) REFERENCES actions(tenant_id,id)
);
CREATE INDEX tickets_list_idx ON tickets(tenant_id,created_at DESC,id DESC);
CREATE INDEX tickets_order_idx ON tickets(tenant_id,order_id,created_at DESC,id DESC);
CREATE TABLE ticket_notes (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, ticket_id UUID NOT NULL,
 content TEXT NOT NULL CHECK(length(content) BETWEEN 1 AND 4000), created_by UUID NOT NULL,
 source_action_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,source_action_id),
 FOREIGN KEY(tenant_id,ticket_id) REFERENCES tickets(tenant_id,id),
 FOREIGN KEY(tenant_id,created_by) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,source_action_id) REFERENCES actions(tenant_id,id)
);
CREATE TABLE ticket_events (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, ticket_id UUID NOT NULL,
 event_type TEXT NOT NULL, actor_user_id UUID NOT NULL, summary TEXT NOT NULL,
 action_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(tenant_id,ticket_id) REFERENCES tickets(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,action_id) REFERENCES actions(tenant_id,id)
);
CREATE INDEX ticket_events_list_idx ON ticket_events(tenant_id,ticket_id,created_at,id);
CREATE TABLE action_receipts (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, action_id UUID NOT NULL,
 tool_name TEXT NOT NULL, business_object_type TEXT NOT NULL, business_object_id UUID NOT NULL,
 result JSONB NOT NULL, executed_by UUID NOT NULL, executed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,action_id), FOREIGN KEY(tenant_id,action_id) REFERENCES actions(tenant_id,id),
 FOREIGN KEY(tenant_id,executed_by) REFERENCES users(tenant_id,id)
);
CREATE TABLE audit_events (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id), actor_user_id UUID NOT NULL,
 operation TEXT NOT NULL, object_type TEXT NOT NULL, object_id UUID NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('agent','manual','user')), action_id UUID,
 summary TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(tenant_id,actor_user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,action_id) REFERENCES actions(tenant_id,id)
);
CREATE INDEX audit_list_idx ON audit_events(tenant_id,created_at DESC,id DESC);
-- +goose StatementBegin
CREATE FUNCTION preserve_action_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.tenant_id,NEW.run_id,NEW.tool_name,NEW.tool_version,NEW.arguments,NEW.request_hash,NEW.payload_digest,NEW.resource_versions,NEW.evidence_versions,NEW.expires_at)
 IS DISTINCT FROM (OLD.tenant_id,OLD.run_id,OLD.tool_name,OLD.tool_version,OLD.arguments,OLD.request_hash,OLD.payload_digest,OLD.resource_versions,OLD.evidence_versions,OLD.expires_at)
 THEN RAISE EXCEPTION 'immutable action snapshot'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER actions_immutable BEFORE UPDATE ON actions FOR EACH ROW EXECUTE FUNCTION preserve_action_snapshot();
-- +goose Down
DROP TABLE audit_events,action_receipts,ticket_events,ticket_notes,tickets,approvals,actions,pending_inputs;
DROP FUNCTION preserve_action_snapshot();
DROP INDEX messages_one_assistant_idx;
ALTER TABLE messages ADD UNIQUE(tenant_id,run_id,role);
ALTER TABLE runs DROP COLUMN wait_expires_at, DROP COLUMN resume_data, DROP COLUMN input_count;
