-- +goose Up
CREATE TABLE run_history_batches (
 id UUID PRIMARY KEY,tenant_id UUID NOT NULL,actor_user_id UUID NOT NULL,
 item_count BIGINT NOT NULL CHECK(item_count BETWEEN 1 AND 10),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_user_id) REFERENCES users(tenant_id,id)
);
CREATE INDEX run_history_batches_page ON run_history_batches(tenant_id,created_at DESC,id DESC);
CREATE TABLE run_history_batch_items (
 id UUID PRIMARY KEY,tenant_id UUID NOT NULL,batch_id UUID NOT NULL,operation_id UUID NOT NULL,
 ordinal BIGINT NOT NULL CHECK(ordinal BETWEEN 1 AND 10),
 UNIQUE(tenant_id,batch_id,ordinal),UNIQUE(tenant_id,operation_id),
 FOREIGN KEY(tenant_id,batch_id) REFERENCES run_history_batches(tenant_id,id),
 FOREIGN KEY(tenant_id,operation_id) REFERENCES run_history_operations(tenant_id,id)
);
-- +goose StatementBegin
CREATE FUNCTION guard_run_history_batch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'RUN_HISTORY_BATCH_IMMUTABLE' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER run_history_batch_guard BEFORE UPDATE OR DELETE ON run_history_batches
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_batch();
CREATE TRIGGER run_history_batch_item_guard BEFORE UPDATE OR DELETE ON run_history_batch_items
 FOR EACH ROW EXECUTE FUNCTION guard_run_history_batch();
-- +goose StatementBegin
CREATE FUNCTION check_run_history_batch_size() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected BIGINT; actual BIGINT; bid UUID; tid UUID;
BEGIN
 IF TG_TABLE_NAME='run_history_batches' THEN bid := NEW.id; ELSE bid := NEW.batch_id; END IF;
 tid := NEW.tenant_id;
 SELECT item_count INTO expected FROM run_history_batches WHERE tenant_id=tid AND id=bid;
 SELECT count(*) INTO actual FROM run_history_batch_items WHERE tenant_id=tid AND batch_id=bid;
 IF expected IS NULL OR actual<>expected OR EXISTS(
  SELECT 1 FROM run_history_batch_items WHERE tenant_id=tid AND batch_id=bid AND ordinal>expected
 ) THEN RAISE EXCEPTION 'RUN_HISTORY_BATCH_INCOMPLETE' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER run_history_batch_size AFTER INSERT ON run_history_batches
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_run_history_batch_size();
CREATE CONSTRAINT TRIGGER run_history_batch_item_size AFTER INSERT ON run_history_batch_items
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_run_history_batch_size();
-- +goose Down
-- Keep durable batch identities, membership, execution fences and original receipts.
SELECT 1;
