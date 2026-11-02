-- +goose Up
ALTER TABLE sessions ADD CONSTRAINT sessions_tenant_id_id_key UNIQUE (tenant_id,id);
CREATE TABLE conversations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    owner_user_id UUID NOT NULL,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 120),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    version BIGINT NOT NULL DEFAULT 1,
    message_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    FOREIGN KEY (tenant_id,owner_user_id) REFERENCES users(tenant_id,id)
);
CREATE INDEX conversations_owner_idx ON conversations(tenant_id,owner_user_id,updated_at DESC,id DESC);
CREATE TABLE runs (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    conversation_id UUID NOT NULL,
    actor_user_id UUID NOT NULL,
    session_id UUID NOT NULL,
    input_message_id UUID NOT NULL,
    result_message_id UUID NOT NULL,
    dispatch_id UUID NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('queued','running','waiting_input','waiting_approval','cancelling','succeeded','failed','cancelled','expired')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    last_event_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_event_seq BETWEEN 0 AND 50000),
    lease_generation BIGINT NOT NULL DEFAULT 1 CHECK (lease_generation > 0),
    lease_expires_at TIMESTAMPTZ,
    deadline_at TIMESTAMPTZ NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    tool_call_count BIGINT NOT NULL DEFAULT 0 CHECK (tool_call_count BETWEEN 0 AND 12),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
    FOREIGN KEY (tenant_id,actor_user_id) REFERENCES users(tenant_id,id)
    ,FOREIGN KEY (tenant_id,session_id) REFERENCES sessions(tenant_id,id)
);
CREATE UNIQUE INDEX runs_one_active_idx ON runs(tenant_id,conversation_id) WHERE status IN ('queued','running','waiting_input','waiting_approval','cancelling');
CREATE INDEX runs_recovery_idx ON runs(status,lease_expires_at,id);
CREATE TABLE messages (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    conversation_id UUID NOT NULL,
    run_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user','assistant')),
    position BIGINT NOT NULL CHECK (position > 0),
    client_message_id UUID,
    content TEXT NOT NULL CHECK (length(content) <= 32000),
    status TEXT NOT NULL CHECK (status IN ('streaming','final','aborted')),
    generation BIGINT NOT NULL DEFAULT 1,
    last_chunk_index BIGINT NOT NULL DEFAULT 0,
    citations JSONB NOT NULL DEFAULT '[]',
    action_receipts JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    UNIQUE (tenant_id,conversation_id,client_message_id),
    UNIQUE (tenant_id,conversation_id,position),
    UNIQUE (tenant_id,run_id,role),
    CHECK ((role='user') = (client_message_id IS NOT NULL)),
    FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
    FOREIGN KEY (tenant_id,run_id) REFERENCES runs(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE runs ADD FOREIGN KEY (tenant_id,input_message_id) REFERENCES messages(tenant_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE runs ADD FOREIGN KEY (tenant_id,result_message_id) REFERENCES messages(tenant_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE run_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    run_id UUID NOT NULL,
    seq BIGINT NOT NULL CHECK (seq BETWEEN 1 AND 50000),
    source_event_id UUID NOT NULL,
    payload_hash TEXT NOT NULL,
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,run_id,seq),
    UNIQUE (tenant_id,run_id,source_event_id),
    FOREIGN KEY (tenant_id,run_id) REFERENCES runs(tenant_id,id)
);
CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    aggregate_id UUID NOT NULL,
    dispatch_id UUID NOT NULL UNIQUE,
    topic TEXT NOT NULL CHECK (topic IN ('run.execute','run.resume','run.cancel','knowledge.index')),
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivering','delivered','dead_letter')),
    attempt_count BIGINT NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_expires_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX outbox_claim_idx ON outbox_events(status,available_at,id) WHERE status IN ('pending','delivering');
CREATE TABLE api_idempotency (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    actor_user_id UUID NOT NULL,
    operation TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_status BIGINT NOT NULL,
    response_body JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '30 days',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,actor_user_id,operation,key_hash),
    FOREIGN KEY (tenant_id,actor_user_id) REFERENCES users(tenant_id,id)
);
CREATE TABLE orders (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_no TEXT NOT NULL,
    masked_customer_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processing','in_transit','delivered','cancelled')),
    total_amount NUMERIC(20,2) NOT NULL CHECK (total_amount >= 0),
    currency TEXT NOT NULL DEFAULT 'CNY',
    version BIGINT NOT NULL DEFAULT 1,
    placed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    UNIQUE (tenant_id,order_no)
);
CREATE TABLE logistics_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    provider_event_id TEXT NOT NULL,
    status TEXT NOT NULL,
    description TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    source_updated_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,provider_event_id),
    FOREIGN KEY (tenant_id,order_id) REFERENCES orders(tenant_id,id)
);
CREATE INDEX logistics_order_idx ON logistics_events(tenant_id,order_id,occurred_at,id);

-- +goose Down
DROP TABLE logistics_events,orders,api_idempotency,outbox_events,run_events;
ALTER TABLE runs DROP CONSTRAINT runs_tenant_id_input_message_id_fkey;
ALTER TABLE runs DROP CONSTRAINT runs_tenant_id_result_message_id_fkey;
DROP TABLE messages,runs,conversations;
ALTER TABLE sessions DROP CONSTRAINT sessions_tenant_id_id_key;
