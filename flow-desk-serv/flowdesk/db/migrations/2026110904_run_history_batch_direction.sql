-- +goose Up
-- The immutable original operations define one direction for every batch.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_run_history_batch_size() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected BIGINT; actual BIGINT; bid UUID; tid UUID; directions BIGINT;
BEGIN
 IF TG_TABLE_NAME='run_history_batches' THEN bid := NEW.id; ELSE bid := NEW.batch_id; END IF;
 tid := NEW.tenant_id;
 SELECT item_count INTO expected FROM run_history_batches WHERE tenant_id=tid AND id=bid;
 SELECT count(*),count(DISTINCT operation.target) INTO actual,directions
 FROM run_history_batch_items item
 JOIN run_history_operations operation ON operation.tenant_id=item.tenant_id AND operation.id=item.operation_id
 WHERE item.tenant_id=tid AND item.batch_id=bid;
 IF expected IS NULL OR actual<>expected OR directions<>1 OR EXISTS(
  SELECT 1 FROM run_history_batch_items WHERE tenant_id=tid AND batch_id=bid AND ordinal>expected
 ) THEN RAISE EXCEPTION 'RUN_HISTORY_BATCH_INCOMPLETE' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Keep immutable membership, direction constraints and all original receipts.
SELECT 1;
