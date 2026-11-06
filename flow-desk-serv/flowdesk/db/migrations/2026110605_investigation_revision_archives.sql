-- +goose Up
-- Keep the old writer -> capture lock order during the guard upgrade.
LOCK TABLE source_investigations,source_investigation_revisions IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE source_investigation_revision_archives (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id), investigation_id UUID NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>0),
 status TEXT NOT NULL CHECK(status IN ('open','investigating','closed')),
 note TEXT NOT NULL CHECK(length(note) BETWEEN 1 AND 2000),
 changed_by UUID NOT NULL, changed_at TIMESTAMPTZ NOT NULL,
 is_baseline BOOLEAN NOT NULL,
 archived_by UUID NOT NULL, archived_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,investigation_id,revision),
 FOREIGN KEY(tenant_id,investigation_id) REFERENCES source_investigations(tenant_id,id),
 FOREIGN KEY(tenant_id,changed_by) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,archived_by) REFERENCES users(tenant_id,id)
);

-- Only an exact original hot snapshot can become a permanent archive.
-- +goose StatementBegin
CREATE FUNCTION verify_investigation_revision_archive() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM source_investigation_revisions h WHERE (h.id,h.tenant_id,h.investigation_id,h.revision,h.status,h.note,h.changed_by,h.changed_at,h.is_baseline)=(NEW.id,NEW.tenant_id,NEW.investigation_id,NEW.revision,NEW.status,NEW.note,NEW.changed_by,NEW.changed_at,NEW.is_baseline)) THEN
  RAISE EXCEPTION 'archive must preserve the complete original investigation snapshot';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER investigation_revision_archive_verify BEFORE INSERT
 ON source_investigation_revision_archives FOR EACH ROW EXECUTE FUNCTION verify_investigation_revision_archive();

-- A successful transaction moves a row; a standalone copy cannot leave two identities.
-- +goose StatementBegin
CREATE FUNCTION complete_investigation_revision_archive() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM source_investigation_revisions h WHERE h.id=NEW.id OR
  (h.tenant_id,h.investigation_id,h.revision)=(NEW.tenant_id,NEW.investigation_id,NEW.revision)) THEN
  RAISE EXCEPTION 'archived investigation snapshot must leave the hot table in the same transaction';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER investigation_revision_archive_complete AFTER INSERT
 ON source_investigation_revision_archives DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION complete_investigation_revision_archive();

-- Hot rows remain immutable; deletion requires their exact, protected archive.
-- +goose StatementBegin
CREATE FUNCTION guard_investigation_revision_move() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM source_investigation_revision_archives a WHERE (a.id,a.tenant_id,a.investigation_id,a.revision,a.status,a.note,a.changed_by,a.changed_at,a.is_baseline)=(OLD.id,OLD.tenant_id,OLD.investigation_id,OLD.revision,OLD.status,OLD.note,OLD.changed_by,OLD.changed_at,OLD.is_baseline)) THEN
   RETURN OLD;
  END IF;
 END IF;
 RAISE EXCEPTION 'investigation snapshots cannot be changed or lost';
END;
$$;
-- +goose StatementEnd
DROP TRIGGER source_investigation_revision_immutable ON source_investigation_revisions;
CREATE TRIGGER source_investigation_revision_immutable BEFORE UPDATE OR DELETE
 ON source_investigation_revisions FOR EACH ROW EXECUTE FUNCTION guard_investigation_revision_move();
CREATE TRIGGER investigation_revision_archive_immutable BEFORE UPDATE OR DELETE
 ON source_investigation_revision_archives FOR EACH ROW EXECUTE FUNCTION reject_source_investigation_revision_mutation();
CREATE TRIGGER investigation_revision_archive_no_truncate BEFORE TRUNCATE
 ON source_investigation_revision_archives FOR EACH STATEMENT EXECUTE FUNCTION reject_source_investigation_revision_mutation();

-- +goose Down
-- Preserve both history tiers and the guards; old readers cannot see archived snapshots.
SELECT 1;
