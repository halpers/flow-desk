-- +goose Up
-- Serialize old writers across baseline capture and trigger installation.
LOCK TABLE source_investigations IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE source_investigation_revisions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenants(id), investigation_id UUID NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>0),
 status TEXT NOT NULL CHECK(status IN ('open','investigating','closed')),
 note TEXT NOT NULL CHECK(length(note) BETWEEN 1 AND 2000),
 changed_by UUID NOT NULL, changed_at TIMESTAMPTZ NOT NULL,
 is_baseline BOOLEAN NOT NULL DEFAULT false,
 UNIQUE(tenant_id,investigation_id,revision),
 FOREIGN KEY(tenant_id,investigation_id) REFERENCES source_investigations(tenant_id,id),
 FOREIGN KEY(tenant_id,changed_by) REFERENCES users(tenant_id,id)
);
-- Preserve only the last known pre-upgrade revision; do not invent missing history.
INSERT INTO source_investigation_revisions
 (tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline)
 SELECT tenant_id,id,revision,status,note,updated_by,updated_at,true FROM source_investigations;

-- +goose StatementBegin
CREATE FUNCTION preserve_source_investigation_revision() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.tenant_id,NEW.object_ref,NEW.store_id,NEW.object_key,NEW.created_by,NEW.created_at)
     IS DISTINCT FROM
     (OLD.id,OLD.tenant_id,OLD.object_ref,OLD.store_id,OLD.object_key,OLD.created_by,OLD.created_at)
     OR NEW.revision<>OLD.revision+1 THEN
   RAISE EXCEPTION 'investigation identity is immutable and revision must advance by one';
  END IF;
 END IF;
 INSERT INTO source_investigation_revisions
  (tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline)
  VALUES(NEW.tenant_id,NEW.id,NEW.revision,NEW.status,NEW.note,NEW.updated_by,NEW.updated_at,false);
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER source_investigation_revision_capture AFTER INSERT OR UPDATE
 ON source_investigations FOR EACH ROW EXECUTE FUNCTION preserve_source_investigation_revision();

-- +goose StatementBegin
CREATE FUNCTION reject_source_investigation_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'investigation revision snapshots are append-only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER source_investigation_revision_immutable BEFORE UPDATE OR DELETE
 ON source_investigation_revisions FOR EACH ROW EXECUTE FUNCTION reject_source_investigation_revision_mutation();
CREATE TRIGGER source_investigation_revision_no_truncate BEFORE TRUNCATE
 ON source_investigation_revisions FOR EACH STATEMENT EXECUTE FUNCTION reject_source_investigation_revision_mutation();

-- +goose Down
-- Keep snapshots, capture/immutability triggers and the migration ledger during application rollback.
SELECT 1;
