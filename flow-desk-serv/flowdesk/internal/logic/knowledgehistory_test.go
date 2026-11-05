package logic

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func metadataRequest(row types.VersionData) *types.ArchiveMetadataReq {
	return &types.ArchiveMetadataReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedDocumentVersion: 1, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func markFixturePurged(t *testing.T, sConn sqlx.SqlConn, id string) {
	t.Helper()
	if _, err := sConn.Exec(`update business.document_versions set cleanup_state='purged',content='',source_bytes=''::bytea,page_texts='[]'::jsonb where id=$1`, id); err != nil {
		t.Fatal(err)
	}
}
func TestMetadataArchiveRollbackReplayAndFences(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	candidate := rows[0]
	markFixturePurged(t, s.Conn, candidate.Id)
	req := metadataRequest(candidate)
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if _, err := ArchiveVersionMetadata(admin, s, req); err == nil {
		t.Fatal("later audit failure accepted")
	}
	stored, err := s.DocumentVersionsModel.FindOne(admin, candidate.Id)
	if err != nil || stored.MetadataArchived || stored.Filename != candidate.Filename {
		t.Fatal("hot detail not rolled back", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.version_metadata_archives"); err != nil || count != 0 {
		t.Fatal("archive rollback", err)
	}
	s.AuditEventsModel = original
	receipt, err := ArchiveVersionMetadata(admin, s, req)
	if err != nil || receipt.ArchivedAt == "" {
		t.Fatal(err)
	}
	replay, err := ArchiveVersionMetadata(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("immutable receipt", err)
	}
	stored, err = s.DocumentVersionsModel.FindOne(admin, candidate.Id)
	if err != nil || !stored.MetadataArchived || stored.Filename != "" || stored.ChunkCount != 0 || stored.JobId == "" || stored.ContentHash != candidate.ContentHash {
		t.Fatal("identity or metadata", err)
	}
	history, err := ListVersionHistory(admin, s, &types.VersionHistoryReq{DocumentId: candidate.DocumentId})
	if err != nil {
		t.Fatal(err)
	}
	item := history.Items[len(history.Items)-1]
	if !item.MetadataArchived || item.Filename != candidate.Filename || item.ChunkCount != 1 || item.CanArchive || item.ArchivedAt != receipt.ArchivedAt {
		t.Fatal("archive detail missing", item)
	}
	hot, err := ListDocumentVersions(admin, s, candidate.DocumentId)
	if err != nil || len(hot.Items) != 3 {
		t.Fatal("hot list", err)
	}
	plan, err := GetRetentionPlan(admin, s, candidate.DocumentId)
	if err != nil || len(plan.Items) != 3 {
		t.Fatal("retention still includes archive", err)
	}
	if _, err = CompleteIndex(admin, s, &types.IndexCompleteReq{VersionId: stored.Id, JobId: stored.JobId, ContentHash: stored.ContentHash, ChunkCount: 1, Status: "ready"}); err == nil {
		t.Fatal("late index callback accepted")
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id, ExpectedVersion: 1}); err == nil {
		t.Fatal("archived version activated")
	}
	if _, err = DownloadOriginalSource(admin, s, &types.VersionPagesReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id}); err == nil {
		t.Fatal("archived original returned")
	}
	changed := *req
	changed.Confirm = false
	if _, err = ArchiveVersionMetadata(admin, s, &changed); err == nil {
		t.Fatal("unconfirmed request")
	}
	changed = *req
	changed.ExpectedDocumentVersion = 2
	_, err = ArchiveVersionMetadata(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	_, err = ArchiveVersionMetadata(admin, s, metadataRequest(candidate))
	assertAccountCode(t, err, "METADATA_ARCHIVE_PROTECTED")
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.metadata_archive'"); err != nil || count != 1 {
		t.Fatal("duplicate audit", count, err)
	}
}

func TestVersionHistoryKeysetBeyondLegacyLimitAndConcurrentArchive(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	document := rows[0].DocumentId
	// Same timestamps force the UUID tie breaker; IDs are disposable synthetic history.
	if _, err := s.Conn.Exec(`insert into business.document_versions
 (id,tenant_id,document_id,job_id,filename,content,content_hash,index_status,chunk_count,cleanup_state,source_size,created_at)
 select gen_random_uuid(),$1,$2,gen_random_uuid(),'synthetic-history-'||n||'.txt','','synthetic-hash','ready',1,'purged',1,'2020-01-01T00:00:00Z'::timestamptz
 from generate_series(1,107) n`, Current(admin).User.TenantId, document); err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		Id string `db:"id"`
	}{}
	if err := s.Conn.QueryRows(&expected, "select id from business.document_versions where document_id=$1 order by created_at desc,id desc", document); err != nil {
		t.Fatal(err)
	}
	first, err := ListVersionHistory(admin, s, &types.VersionHistoryReq{DocumentId: document, Limit: 11})
	if err != nil || !first.HasMore || len(first.Items) != 11 {
		t.Fatal("first", err)
	}
	selected := first.Items[len(first.Items)-1]
	if _, err = ArchiveVersionMetadata(admin, s, metadataRequest(types.VersionData{Id: selected.Id, DocumentId: document})); err != nil {
		t.Fatal(err)
	}
	// A newly uploaded version sorts before the cursor, so does not displace older pages.
	if _, err = UploadDocumentVersion(admin, s, &types.UploadVersionReq{DocumentId: document, Filename: "new-after-page.txt", Content: "Synthetic new page", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	for _, row := range first.Items {
		actual = append(actual, row.Id)
	}
	cursor := first.NextCursor
	for pageNumber := 0; cursor != "" && pageNumber < 30; pageNumber++ {
		page, readErr := ListVersionHistory(admin, s, &types.VersionHistoryReq{DocumentId: document, Limit: 11, Cursor: cursor})
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, row := range page.Items {
			actual = append(actual, row.Id)
		}
		cursor = page.NextCursor
	}
	expectedIDs := []string{}
	for _, row := range expected {
		expectedIDs = append(expectedIDs, row.Id)
	}
	if !slices.Equal(actual, expectedIDs) || len(actual) != 111 {
		t.Fatal("pagination omitted or duplicated history", len(actual))
	}
	for _, request := range []types.VersionHistoryReq{
		{DocumentId: document, Limit: 51}, {DocumentId: document, Limit: -1}, {DocumentId: document, Cursor: "malformed"},
		{DocumentId: document, Cursor: encodeCursor("foreign-scope", "2020-01-01T00:00:00Z|"+selected.Id)},
		{DocumentId: document, Cursor: encodeCursor("version-history-v1:"+Current(admin).User.TenantId+":"+Current(admin).User.Id+":"+document, "bad|"+selected.Id)},
	} {
		if _, err = ListVersionHistory(admin, s, &request); err == nil {
			t.Fatal("invalid history request accepted")
		}
	}
}

func TestMetadataArchiveProtectedStatesAndStaleDocument(t *testing.T) {
	s, operator, admin, rows := retentionFixture(t)
	row := rows[0]
	_, err := ArchiveVersionMetadata(admin, s, metadataRequest(row))
	assertAccountCode(t, err, "METADATA_ARCHIVE_PROTECTED")
	markFixturePurged(t, s.Conn, row.Id)
	req := metadataRequest(row)
	req.ExpectedDocumentVersion = 2
	_, err = ArchiveVersionMetadata(admin, s, req)
	assertAccountCode(t, err, "VERSION_CONFLICT")
	for _, setting := range []struct{ set, undo string }{
		{"update business.documents set active_version_id=$1 where id=$2", "update business.documents set active_version_id=null where id=$2 and $1::uuid is not null"},
		{"update business.document_versions set source_epoch=1 where id=$1 and document_id=$2", "update business.document_versions set source_epoch=0 where id=$1 and document_id=$2"},
		{"update business.outbox_events set status='pending' where payload->>'version_id'=$1 and aggregate_id=$2", "update business.outbox_events set status='delivered' where payload->>'version_id'=$1 and aggregate_id=$2"},
	} {
		if _, err = s.Conn.Exec(setting.set, row.Id, row.DocumentId); err != nil {
			t.Fatal(err)
		}
		_, err = ArchiveVersionMetadata(admin, s, metadataRequest(row))
		assertAccountCode(t, err, "METADATA_ARCHIVE_PROTECTED")
		if _, err = s.Conn.Exec(setting.undo, row.Id, row.DocumentId); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := CreateConversation(operator, s, &types.CreateConversationReq{IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run, err := SendMessage(operator, s, &types.SendMessageReq{ConversationId: conversation.Id, ClientMessageId: uuid.NewString(), Content: "Synthetic reference", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec(`insert into business.run_evidence(id,tenant_id,run_id,version_id,chunk_id,title,excerpt,content_hash)
 values($1,$2,$3,$4,$5,'Synthetic','Synthetic reference',$6)`, uuid.NewString(), Current(admin).User.TenantId, run.Id, row.Id, uuid.NewString(), row.ContentHash); err != nil {
		t.Fatal(err)
	}
	_, err = ArchiveVersionMetadata(admin, s, metadataRequest(row))
	assertAccountCode(t, err, "METADATA_ARCHIVE_PROTECTED")
}

func TestMetadataHistoryCurrentAuthorizationAndRevokedReplay(t *testing.T) {
	s, operator, admin, rows := retentionFixture(t)
	row := rows[0]
	markFixturePurged(t, s.Conn, row.Id)
	historyReq := &types.VersionHistoryReq{DocumentId: row.DocumentId, Limit: 1}
	if _, err := ListVersionHistory(operator, s, historyReq); err == nil {
		t.Fatal("operator history")
	}
	if _, err := ArchiveVersionMetadata(operator, s, metadataRequest(row)); err == nil {
		t.Fatal("operator archive")
	}
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "History manager", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	account := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	req := metadataRequest(row)
	if _, err = ArchiveVersionMetadata(manager, s, req); err != nil {
		t.Fatal(err)
	}
	doc, err := s.DocumentsModel.FindOne(admin, row.DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.KnowledgeBasesModel.FindOne(admin, doc.KnowledgeBaseId)
	if err != nil {
		t.Fatal(err)
	}
	team := newTeam(t, s, admin)
	baseData, err := SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveVersionMetadata(manager, s, req); err == nil {
		t.Fatal("team revoked replay")
	}
	if _, err = ListVersionHistory(manager, s, historyReq); err == nil {
		t.Fatal("team revoked history")
	}
	memberChange(t, s, admin, team, account.Id, true)
	if _, err = ArchiveVersionMetadata(manager, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: baseData.AclRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveVersionMetadata(manager, s, req); err == nil {
		t.Fatal("ACL revoked replay")
	}
	if _, err = ListVersionHistory(manager, s, historyReq); err == nil {
		t.Fatal("ACL revoked history")
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin", "operator"}, ExpectedRevision: baseData.AclRevision + 1}); err != nil {
		t.Fatal(err)
	}
	page, err := ListVersionHistory(manager, s, historyReq)
	if err != nil || page.NextCursor == "" {
		t.Fatal(err)
	}
	foreign := *Current(admin).User
	foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.history"
	if _, err = s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-history", Name: "Synthetic foreign history"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignCtx, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	if _, err = ListVersionHistory(foreignCtx, s, historyReq); err == nil {
		t.Fatal("cross tenant history")
	}
	if _, err = ArchiveVersionMetadata(foreignCtx, s, req); err == nil {
		t.Fatal("cross tenant archive")
	}
	if _, err = ListVersionHistory(admin, s, &types.VersionHistoryReq{DocumentId: row.DocumentId, Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor reused by another viewer")
	}
	if _, err = s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(manager).Session.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveVersionMetadata(manager, s, req); err == nil {
		t.Fatal("session revoked replay")
	}
	if _, err = ListVersionHistory(manager, s, historyReq); err == nil {
		t.Fatal("session revoked history")
	}
}

type pausedMetadataAudit struct {
	model.AuditEventsModel
	entered, release chan struct{}
}

func (m pausedMetadataAudit) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if row.Operation == "knowledge.metadata_archive" {
		close(m.entered)
		<-m.release
	}
	return m.AuditEventsModel.InsertTx(ctx, tx, row)
}
func TestMetadataArchiveSerializesHistoryAndSingleEffect(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	row := rows[0]
	markFixturePurged(t, s.Conn, row.Id)
	entered, release := make(chan struct{}), make(chan struct{})
	shadow := *s
	shadow.AuditEventsModel = pausedMetadataAudit{s.AuditEventsModel, entered, release}
	updated := make(chan error, 1)
	go func() { _, e := ArchiveVersionMetadata(admin, &shadow, metadataRequest(row)); updated <- e }()
	select {
	case <-entered:
	case e := <-updated:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("archive lock timeout")
	}
	read := make(chan error, 1)
	go func() {
		page, e := ListVersionHistory(admin, s, &types.VersionHistoryReq{DocumentId: row.DocumentId})
		if e == nil && !page.Items[len(page.Items)-1].MetadataArchived {
			e = fmt.Errorf("partial archive visible")
		}
		read <- e
	}()
	select {
	case e := <-read:
		close(release)
		<-updated
		t.Fatal("history crossed transaction", e)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history failed to resume")
	}
}
