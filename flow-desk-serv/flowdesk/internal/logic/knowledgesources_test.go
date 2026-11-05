package logic

import (
	"context"
	"encoding/base64"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type memorySources struct {
	objects     map[string][]byte
	puts, reads int
	losePut     bool
	afterRead   func()
}

func (m *memorySources) ID() string { return "synthetic-store" }
func (m *memorySources) Read(_ context.Context, key string, _ int64) ([]byte, error) {
	m.reads++
	raw, ok := m.objects[key]
	if m.afterRead != nil {
		hook := m.afterRead
		m.afterRead = nil
		hook()
	}
	if !ok {
		return nil, client.ErrSourceMissing
	}
	return raw, nil
}
func (m *memorySources) PutIfAbsent(_ context.Context, key string, raw []byte) error {
	m.puts++
	if _, ok := m.objects[key]; !ok {
		m.objects[key] = append([]byte(nil), raw...)
	}
	if m.losePut {
		m.losePut = false
		return client.ErrSourceUnavailable
	}
	return nil
}
func sourceFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *model.OutboxEvents, *memorySources) {
	t.Helper()
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	store := &memorySources{objects: map[string][]byte{}}
	s.SourceStore = store
	s.Config.FileStoreProvider = "s3"
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic sources", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	request := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Source", Filename: "source.pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nsynthetic\x00\xff")), IdempotencyKey: uuid.NewString()}
	version, err := UploadDocument(admin, s, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := UploadDocument(admin, s, request)
	if err != nil || replay.Id != version.Id {
		t.Fatal("upload replay", err)
	}
	row, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if err != nil {
		t.Fatal(err)
	}
	var event model.OutboxEvents
	if err = s.Conn.QueryRow(&event, "select * from business.outbox_events where aggregate_id=$1 and topic='knowledge.archive'", row.DocumentId); err != nil {
		t.Fatal(err)
	}
	err = s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id)
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.OutboxEventsModel.FindOne(context.Background(), event.Id)
	if err != nil {
		t.Fatal(err)
	}
	return s, admin, operator, row, claimed, store
}
func sourceKey(row *model.DocumentVersions) string {
	return "knowledge/" + row.TenantId + "/" + row.Id + "/" + row.ContentHash + ".pdf"
}
func sourceRequest(row *model.DocumentVersions) *types.VersionPagesReq {
	return &types.VersionPagesReq{DocumentId: row.DocumentId, VersionId: row.Id}
}
func assertSource(t *testing.T, s *svc.ServiceContext, admin context.Context, row *model.DocumentVersions, storage string) {
	t.Helper()
	current, err := s.DocumentVersionsModel.FindOne(context.Background(), row.Id)
	if err != nil || current.SourceStorage != storage || current.SourceSize != int64(len(row.SourceBytes)) {
		t.Fatal("source locator/size", err)
	}
	if storage == "s3" && len(current.SourceBytes) != 0 {
		t.Fatal("database bytes retained after archive")
	}
	downloaded, err := DownloadOriginalSource(admin, s, sourceRequest(row))
	if err != nil || downloaded.ContentBase64 != base64.StdEncoding.EncodeToString(row.SourceBytes) {
		t.Fatal("download mismatch", err)
	}
	source, err := GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: row.JobId})
	if err != nil || source.SourceBase64 != downloaded.ContentBase64 {
		t.Fatal("index read adapter", err)
	}
}
func TestSourceArchiveLostResponseRestoreAndGeneration(t *testing.T) {
	s, admin, _, row, event, store := sourceFixture(t)
	assertSource(t, s, admin, row, "postgres")
	store.losePut = true
	if err := ArchiveOriginalSource(context.Background(), s, event); err == nil {
		t.Fatal("expected lost response")
	}
	assertSource(t, s, admin, row, "postgres")
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	assertSource(t, s, admin, row, "s3")
	if store.puts != 1 {
		t.Fatal("retry overwrote object")
	}
	req := &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}
	restored, err := RestoreOriginalSource(admin, s, req)
	if err != nil || restored.SourceStorage != "postgres" {
		t.Fatal("restore", err)
	}
	if _, err = RestoreOriginalSource(admin, s, req); err != nil {
		t.Fatal("restore replay", err)
	}
	if err = ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal("old archive ack", err)
	}
	assertSource(t, s, admin, row, "postgres")
	req.IdempotencyKey = uuid.NewString()
	if _, err = RequestSourceArchive(admin, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err = RequestSourceArchive(admin, s, req); err != nil {
		t.Fatal("archive request replay", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.outbox_events where aggregate_id=$1 and topic='knowledge.archive'", row.DocumentId); err != nil || count != 2 {
		t.Fatal("archive generation", count, err)
	}
}
func TestSourceArchiveLeaseFencingAndIntegrity(t *testing.T) {
	s, admin, _, row, event, store := sourceFixture(t)
	store.afterRead = func() {
		if _, err := s.Conn.Exec("update business.outbox_events set attempt_count=attempt_count+1 where id=$1", event.Id); err != nil {
			t.Fatal(err)
		}
	}
	if err := ArchiveOriginalSource(context.Background(), s, event); err == nil {
		t.Fatal("stale worker committed locator")
	}
	assertSource(t, s, admin, row, "postgres")
	event.AttemptCount++
	store.objects[sourceKey(row)] = []byte("corrupted")
	if err := ArchiveOriginalSource(context.Background(), s, event); err == nil {
		t.Fatal("wrong content archived")
	}
	if store.puts != 1 {
		t.Fatal("existing object overwritten")
	}
	store.objects[sourceKey(row)] = row.SourceBytes
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	assertSource(t, s, admin, row, "s3")
	delete(store.objects, sourceKey(row))
	if _, err := DownloadOriginalSource(admin, s, sourceRequest(row)); err == nil {
		t.Fatal("missing object returned")
	}
	store.objects[sourceKey(row)] = []byte("corrupted")
	if _, err := DownloadOriginalSource(admin, s, sourceRequest(row)); err == nil {
		t.Fatal("corrupt object returned")
	}
}
func TestSourceDownloadAuthorizationRecheck(t *testing.T) {
	s, admin, operator, row, event, store := sourceFixture(t)
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	before := store.reads
	if _, err := DownloadOriginalSource(operator, s, sourceRequest(row)); err == nil {
		t.Fatal("operator downloaded original")
	}
	wrong := *sourceRequest(row)
	wrong.DocumentId = uuid.NewString()
	if _, err := DownloadOriginalSource(admin, s, &wrong); err == nil {
		t.Fatal("document binding bypass")
	}
	foreignUser := *Current(admin).User
	foreignUser.TenantId = uuid.NewString()
	foreignIdentity := *Current(admin)
	foreignIdentity.User = &foreignUser
	if _, err := DownloadOriginalSource(WithIdentity(context.Background(), &foreignIdentity), s, sourceRequest(row)); err == nil {
		t.Fatal("cross tenant download")
	}
	if store.reads != before {
		t.Fatal("unauthorized request accessed storage")
	}
	store.afterRead = func() {
		if _, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(admin).User.Id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DownloadOriginalSource(admin, s, sourceRequest(row)); err == nil {
		t.Fatal("revoked admin received source")
	}
}

type failedArchiveModel struct{ model.DocumentVersionsModel }

func (m failedArchiveModel) UpdateArchivedSourceTx(ctx context.Context, tx sqlx.Session, row *model.DocumentVersions, store, key string) error {
	if err := m.DocumentVersionsModel.UpdateArchivedSourceTx(ctx, tx, row, store, key); err != nil {
		return err
	}
	return sourceUnavailable()
}
func TestSourceArchiveDatabaseRollbackKeepsOriginal(t *testing.T) {
	s, admin, _, row, event, store := sourceFixture(t)
	originalModel := s.DocumentVersionsModel
	s.DocumentVersionsModel = failedArchiveModel{originalModel}
	if err := ArchiveOriginalSource(context.Background(), s, event); err == nil {
		t.Fatal("expected rollback")
	}
	s.DocumentVersionsModel = originalModel
	assertSource(t, s, admin, row, "postgres")
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	assertSource(t, s, admin, row, "s3")
	if store.puts != 1 {
		t.Fatal("rollback recovery rewrote immutable object")
	}
}

func TestSourceDeadLetterRequeuePreservesFenceAndPausedReads(t *testing.T) {
	s, admin, _, row, event, store := sourceFixture(t)
	if _, err := s.Conn.Exec("update business.outbox_events set status='dead_letter',attempt_count=8,lease_expires_at=null where id=$1", event.Id); err != nil {
		t.Fatal(err)
	}
	req := &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}
	if _, err := RequestSourceArchive(admin, s, req); err != nil {
		t.Fatal(err)
	}
	if err := s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id)
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.OutboxEventsModel.FindOne(context.Background(), event.Id)
	if err != nil || claimed.AttemptCount != 9 {
		t.Fatal("fencing counter reset", err)
	}
	event.AttemptCount = 8
	if err = ArchiveOriginalSource(context.Background(), s, event); err == nil {
		t.Fatal("old dead-letter worker committed")
	}
	if err = ArchiveOriginalSource(context.Background(), s, claimed); err != nil {
		t.Fatal(err)
	}
	s.Config.FileStoreProvider = "postgres"
	assertSource(t, s, admin, row, "s3")
	s.SourceStore = &memorySources{objects: store.objects}
	current, err := s.DocumentVersionsModel.FindOne(context.Background(), row.Id)
	if err != nil {
		t.Fatal(err)
	}
	current.SourceStoreId = "other-store"
	if _, err = readOriginal(context.Background(), s, current); err == nil {
		t.Fatal("store fingerprint mismatch accepted")
	}
	store.afterRead = func() {
		if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
			t.Fatal(err)
		}
	}
	s.SourceStore = store
	if _, err = DownloadOriginalSource(admin, s, sourceRequest(row)); err == nil {
		t.Fatal("revoked session received source")
	}
}

func TestSourceMigrationSupportsLegacyInserts(t *testing.T) {
	s, _, _, row, _, _ := sourceFixture(t)
	var size int64
	err := s.Conn.QueryRow(&size, `insert into business.document_versions(id,tenant_id,document_id,job_id,filename,content,content_hash,index_status,source_format,source_bytes)
 values($1,$2,$3,$4,'legacy.pdf','',$5,'pending','pdf',$6) returning source_size`, uuid.NewString(), row.TenantId, row.DocumentId, uuid.NewString(), row.ContentHash, row.SourceBytes)
	if err != nil || size != int64(len(row.SourceBytes)) {
		t.Fatal("old insert compatibility", size, err)
	}
}
