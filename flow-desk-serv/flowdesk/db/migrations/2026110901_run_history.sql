-- +goose Up
CREATE TABLE run_history_operations (
 id UUID PRIMARY KEY,tenant_id UUID NOT NULL,run_id UUID NOT NULL,actor_user_id UUID NOT NULL,
 generation BIGINT NOT NULL CHECK(generation>0),target TEXT NOT NULL CHECK(target IN ('live','archived')),
 status TEXT NOT NULL CHECK(status IN ('pending','completed')),
 checkpoint_count BIGINT NOT NULL DEFAULT 0,checkpoint_blob_count BIGINT NOT NULL DEFAULT 0,
 checkpoint_write_count BIGINT NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 completed_at TIMESTAMPTZ,UNIQUE(tenant_id,run_id,generation),UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_user_id) REFERENCES users(tenant_id,id)
);
CREATE UNIQUE INDEX run_history_pending ON run_history_operations(tenant_id,run_id) WHERE status='pending';
CREATE TABLE message_payload_archives (
 id UUID PRIMARY KEY,tenant_id UUID NOT NULL,run_id UUID NOT NULL,content TEXT NOT NULL,
 citations JSONB NOT NULL,action_receipts JSONB NOT NULL,
 FOREIGN KEY(tenant_id,id) REFERENCES messages(tenant_id,id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id)
);
CREATE TABLE run_event_payload_archives (
 id UUID PRIMARY KEY,tenant_id UUID NOT NULL,run_id UUID NOT NULL,payload JSONB NOT NULL,
 FOREIGN KEY(id) REFERENCES run_events(id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id)
);
CREATE INDEX message_payload_archive_run ON message_payload_archives(tenant_id,run_id);
CREATE INDEX run_event_payload_archive_run ON run_event_payload_archives(tenant_id,run_id);
CREATE VIEW messages_history_read AS SELECT m.id,m.tenant_id,m.conversation_id,m.run_id,m.role,
 m.position,m.client_message_id,coalesce(a.content,m.content) AS content,m.status,m.generation,
 m.last_chunk_index,coalesce(a.citations,m.citations) AS citations,
 coalesce(a.action_receipts,m.action_receipts) AS action_receipts,m.created_at,m.updated_at
 FROM messages m LEFT JOIN message_payload_archives a ON a.tenant_id=m.tenant_id AND a.id=m.id;
CREATE VIEW run_events_history_read AS SELECT e.id,e.tenant_id,e.run_id,e.seq,e.source_event_id,
 e.payload_hash,e.type,coalesce(a.payload,e.payload) AS payload,e.occurred_at,e.created_at
 FROM run_events e LEFT JOIN run_event_payload_archives a ON a.tenant_id=e.tenant_id AND a.id=e.id;
-- +goose StatementBegin
CREATE FUNCTION guard_run_history_payload() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid UUID; allowed UUID;
BEGIN
 rid := CASE WHEN TG_OP='DELETE' THEN OLD.run_id ELSE NEW.run_id END;
 SELECT id INTO allowed FROM run_history_operations WHERE run_id=rid
   AND status='pending' ORDER BY generation DESC LIMIT 1;
 IF EXISTS(SELECT 1 FROM run_history_operations WHERE run_id=rid) AND
    (allowed IS NULL OR current_setting('flowdesk.history_operation',true) IS DISTINCT FROM allowed::text) THEN
   RAISE EXCEPTION 'RUN_HISTORY_FENCED' USING ERRCODE='23514';
 END IF;
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;
-- +goose StatementEnd
CREATE TRIGGER run_history_message_guard BEFORE INSERT OR UPDATE OR DELETE ON messages
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_payload();
CREATE TRIGGER run_history_event_guard BEFORE INSERT OR UPDATE OR DELETE ON run_events
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_payload();
CREATE TRIGGER run_history_cold_message_guard BEFORE INSERT OR UPDATE OR DELETE ON message_payload_archives
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_payload();
CREATE TRIGGER run_history_cold_event_guard BEFORE INSERT OR UPDATE OR DELETE ON run_event_payload_archives
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_payload();
-- +goose StatementBegin
CREATE FUNCTION guard_run_history_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status='completed' OR NEW.id<>OLD.id OR NEW.tenant_id<>OLD.tenant_id
 OR NEW.run_id<>OLD.run_id OR NEW.actor_user_id<>OLD.actor_user_id
 OR NEW.generation<>OLD.generation OR NEW.target<>OLD.target OR NEW.created_at<>OLD.created_at THEN
  RAISE EXCEPTION 'RUN_HISTORY_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER run_history_operation_guard BEFORE UPDATE OR DELETE ON run_history_operations
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_operation();
-- +goose StatementBegin
CREATE FUNCTION guard_run_history_run() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid UUID;
BEGIN
 rid := CASE WHEN TG_OP='INSERT' THEN NEW.retry_of_run_id ELSE OLD.id END;
 IF EXISTS(SELECT 1 FROM run_history_operations WHERE run_id=rid) THEN
  RAISE EXCEPTION 'RUN_HISTORY_FENCED' USING ERRCODE='23514';
 END IF;
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;
-- +goose StatementEnd
CREATE TRIGGER run_history_run_guard BEFORE INSERT OR UPDATE OR DELETE ON runs
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_run();
-- +goose Down
-- Preserve payloads, fences, receipts and forward-compatible reads.
SELECT 1;
