package logic

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type retiringSources struct {
	*memorySources
	retirements     int
	unsupported     bool
	loseResponse    bool
	afterRetirement func()
}

func (m *retiringSources) CheckRetirement(context.Context) error {
	if m.unsupported {
		return client.ErrSourceCleanupUnsupported
	}
	return nil
}
func (m *retiringSources) Retire(_ context.Context, key, hash string, size int64, id string) error {
	m.retirements++
	if raw, exists := m.objects[key]; exists && len(raw) > 0 && (int64(len(raw)) != size || HashToken(string(raw)) != hash) {
		return client.ErrSourceIntegrity
	}
	m.objects[key] = []byte{}
	if m.afterRetirement != nil {
		hook := m.afterRetirement
		m.afterRetirement = nil
		hook()
	}
	if m.loseResponse {
		m.loseResponse = false
		return client.ErrSourceUnavailable
	}
	return nil
}
func sourceCleanupFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *retiringSources, *types.CleanupArchiveCopyReq) {
	t.Helper()
	s, admin, operator, row, event, store := sourceFixture(t)
	s.Config.FileStoreCleanupEnabled = true
	cleaner := &retiringSources{memorySources: store}
	s.SourceStore = cleaner
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	plan, err := ListArchiveCopies(admin, s, sourceRequest(row))
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Eligible || !slices.Contains(plan.Items[0].Reasons, "restore_required") {
		t.Fatal("current archive protection", plan, err)
	}
	if _, err = RestoreOriginalSource(admin, s, &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	return s, admin, operator, row, cleaner, &types.CleanupArchiveCopyReq{DocumentId: row.DocumentId, VersionId: row.Id, ObjectId: plan.Items[0].Id, Confirm: true, ExpectedSourceEpoch: 1, IdempotencyKey: uuid.NewString()}
}
func claimSourceCleanup(t *testing.T, s *svc.ServiceContext, id string) *model.OutboxEvents {
	t.Helper()
	var event model.OutboxEvents
	if err := s.Conn.QueryRow(&event, "select * from business.outbox_events where topic='knowledge.source_cleanup' and payload->>'object_id'=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id)
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.OutboxEventsModel.FindOne(context.Background(), event.Id)
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}
func TestSourceCleanupLostResponseLeaseAndIndependentGeneration(t *testing.T) {
	s, admin, _, row, store, req := sourceCleanupFixture(t)
	if _, err := CleanupArchiveCopy(admin, s, req); err != nil {
		t.Fatal(err)
	}
	event := claimSourceCleanup(t, s, req.ObjectId)
	if _, err := s.Conn.Exec("update business.outbox_events set attempt_count=attempt_count+1 where id=$1", event.Id); err != nil {
		t.Fatal(err)
	}
	if err := CleanupSourceObject(context.Background(), s, event); err == nil || store.retirements != 0 {
		t.Fatal("stale owner accessed store", err)
	}
	event.AttemptCount++
	store.loseResponse = true
	if err := CleanupSourceObject(context.Background(), s, event); err == nil {
		t.Fatal("expected lost response")
	}
	object, err := s.SourceObjectsModel.FindOne(context.Background(), req.ObjectId)
	if err != nil || object.Status != sourceObjectCleanupPending {
		t.Fatal("premature completion", err)
	}
	if err = CleanupSourceObject(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	replay, err := CleanupArchiveCopy(admin, s, req)
	if err != nil || replay.Status != sourceObjectRetired {
		t.Fatal("durable replay", replay, err)
	}
	assertSource(t, s, admin, row, "postgres")
	// Late immutable upload cannot replace the marker.
	if err = store.PutIfAbsent(context.Background(), sourceKey(row), row.SourceBytes); err != nil || len(store.objects[sourceKey(row)]) != 0 {
		t.Fatal("late resurrection", err)
	}
	archiveReq := &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}
	if _, err = RequestSourceArchive(admin, s, archiveReq); err != nil {
		t.Fatal(err)
	}
	var next model.OutboxEvents
	if err = s.Conn.QueryRow(&next, "select * from business.outbox_events where topic='knowledge.archive' and aggregate_id=$1 and (payload->>'source_epoch')::int=1", row.DocumentId); err != nil {
		t.Fatal(err)
	}
	if err = s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, next.Id)
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.OutboxEventsModel.FindOne(context.Background(), next.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = ArchiveOriginalSource(context.Background(), s, claimed); err != nil {
		t.Fatal(err)
	}
	assertSource(t, s, admin, row, "s3")
	if err = CleanupSourceObject(context.Background(), s, event); err != nil {
		t.Fatal("retired replay", err)
	}
	assertSource(t, s, admin, row, "s3")
}
func TestSourceCleanupAuthorizationIntegrityAndRollback(t *testing.T) {
	s, admin, operator, row, store, req := sourceCleanupFixture(t)
	if _, err := ListArchiveCopies(operator, s, sourceRequest(row)); err == nil {
		t.Fatal("operator preview")
	}
	if _, err := CleanupArchiveCopy(operator, s, req); err == nil {
		t.Fatal("operator cleanup")
	}
	tenant := uuid.NewString()
	if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: tenant, Code: "source-foreign", Name: "Synthetic foreign"}); err != nil {
		t.Fatal(err)
	}
	foreign := *Current(admin).User
	foreign.Id = uuid.NewString()
	foreign.TenantId = tenant
	foreign.Account = "source.foreign"
	if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignAdmin, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	if _, err := ListArchiveCopies(foreignAdmin, s, sourceRequest(row)); err == nil {
		t.Fatal("cross tenant preview")
	}
	if _, err := CleanupArchiveCopy(foreignAdmin, s, req); err == nil {
		t.Fatal("cross tenant cleanup")
	}
	wrong := *req
	wrong.DocumentId = uuid.NewString()
	if _, err := CleanupArchiveCopy(admin, s, &wrong); err == nil {
		t.Fatal("document binding")
	}
	wrong = *req
	wrong.ObjectId = uuid.NewString()
	if _, err := CleanupArchiveCopy(admin, s, &wrong); err == nil {
		t.Fatal("object binding")
	}
	wrong = *req
	wrong.ExpectedSourceEpoch = 2
	if _, err := CleanupArchiveCopy(admin, s, &wrong); err == nil {
		t.Fatal("stale generation")
	}
	s.Config.FileStoreCleanupEnabled = false
	if _, err := CleanupArchiveCopy(admin, s, req); err == nil {
		t.Fatal("disabled cleanup")
	}
	s.Config.FileStoreCleanupEnabled = true
	store.unsupported = true
	if _, err := CleanupArchiveCopy(admin, s, req); err == nil {
		t.Fatal("unsupported bucket")
	}
	store.unsupported = false
	if _, err := s.Conn.Exec("update business.document_versions set source_bytes=$2 where id=$1", row.Id, make([]byte, len(row.SourceBytes))); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupArchiveCopy(admin, s, req); err == nil {
		t.Fatal("invalid database original")
	}
	if _, err := s.Conn.Exec("update business.document_versions set source_bytes=$2 where id=$1", row.Id, row.SourceBytes); err != nil {
		t.Fatal(err)
	}
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if _, err := CleanupArchiveCopy(admin, s, req); err == nil {
		t.Fatal("late audit failure")
	}
	s.AuditEventsModel = original
	object, err := s.SourceObjectsModel.FindOne(context.Background(), req.ObjectId)
	if err != nil || object.Status != sourceObjectRegistered {
		t.Fatal("state rollback", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.outbox_events where topic='knowledge.source_cleanup'"); err != nil || count != 0 {
		t.Fatal("outbox rollback", count, err)
	}
	if _, err = CleanupArchiveCopy(admin, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.users set role='operator' where id=$1", Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = CleanupArchiveCopy(admin, s, req); err == nil {
		t.Fatal("revoked admin replay")
	}
	if store.retirements != 0 {
		t.Fatal("request accessed object bytes")
	}
}
func TestSourceCleanupLeaseLossAfterRetirementRecovers(t *testing.T) {
	s, admin, _, row, store, req := sourceCleanupFixture(t)
	if _, err := CleanupArchiveCopy(admin, s, req); err != nil {
		t.Fatal(err)
	}
	event := claimSourceCleanup(t, s, req.ObjectId)
	store.afterRetirement = func() {
		if _, err := s.Conn.Exec("update business.outbox_events set attempt_count=attempt_count+1 where id=$1", event.Id); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanupSourceObject(context.Background(), s, event); err == nil {
		t.Fatal("lost lease committed")
	}
	object, err := s.SourceObjectsModel.FindOne(context.Background(), req.ObjectId)
	if err != nil || object.Status != sourceObjectCleanupPending {
		t.Fatal("lost lease state", err)
	}
	event.AttemptCount++
	if err = CleanupSourceObject(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	assertSource(t, s, admin, row, "postgres")
	if _, err = s.Conn.Exec("update business.outbox_events set attempt_count=1000 where id=$1", event.Id); err != nil {
		t.Fatal(err)
	}
	if err = s.OutboxEventsModel.UpdateRetry(context.Background(), event.Id, 1000, "synthetic"); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.OutboxEventsModel.FindOne(context.Background(), event.Id)
	if err != nil || fresh.Status != "pending" {
		t.Fatal("cleanup must keep retrying", err)
	}
}

func TestSourceObjectMigrationBackfillsLegacyLocator(t *testing.T) {
	tenant, base, document, version := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw := []byte("%PDF synthetic legacy archive")
	hash := HashToken(string(raw))
	key := "knowledge/" + tenant + "/" + version + "/" + hash + ".pdf"
	s := testService(t, func(db *sql.DB) {
		if _, err := db.Exec("insert into tenants(id,code,name)values($1,'source-legacy','Synthetic legacy')", tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("insert into knowledge_bases(id,tenant_id,name)values($1,$2,'Synthetic legacy')", base, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("insert into documents(id,tenant_id,knowledge_base_id,title)values($1,$2,$3,'Synthetic legacy')", document, tenant, base); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("insert into document_versions(id,tenant_id,document_id,job_id,filename,content,content_hash,source_format,source_storage,source_size,source_store_id,source_key,source_epoch)values($1,$2,$3,$4,'legacy.pdf','',$5,'pdf','s3',$6,'synthetic-store',$7,4)", version, tenant, document, uuid.NewString(), hash, len(raw), key); err != nil {
			t.Fatal(err)
		}
	})
	object, err := s.SourceObjectsModel.FindOneByTenantIdId(context.Background(), tenant, version)
	if err != nil || object.VersionId != version || object.SourceEpoch != 4 || object.ObjectKey != key || object.ContentHash != hash || object.SourceSize != int64(len(raw)) || object.Status != sourceObjectRegistered {
		t.Fatal("legacy object registration", object, err)
	}
}
