package logic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func retentionFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, []types.VersionData) {
	t.Helper()
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic retention", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	rows := []types.VersionData{}
	for index := range 4 {
		name := fmt.Sprintf("policy-%d.txt", index)
		content := fmt.Sprintf("Synthetic policy revision %d", index)
		var row types.VersionData
		if index == 0 {
			row, err = UploadDocument(admin, s, &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Retention", Filename: name, Content: content, IdempotencyKey: uuid.NewString()})
		} else {
			row, err = UploadDocumentVersion(admin, s, &types.UploadVersionReq{DocumentId: rows[0].DocumentId, Filename: name, Content: content, IdempotencyKey: uuid.NewString()})
		}
		if err != nil {
			t.Fatal(err)
		}
		stored, err := s.DocumentVersionsModel.FindOne(admin, row.Id)
		if err != nil {
			t.Fatal(err)
		}
		row, err = CompleteIndex(admin, s, &types.IndexCompleteReq{VersionId: row.Id, JobId: stored.JobId, ContentHash: row.ContentHash, ChunkCount: 1, Status: "ready"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Conn.Exec("update business.document_versions set created_at=now()-interval '40 days'+$2::int*interval '1 minute' where id=$1", row.Id, index); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if _, err = s.Conn.Exec("update business.outbox_events set status='delivered' where aggregate_id=$1 and topic='knowledge.index'", rows[0].DocumentId); err != nil {
		t.Fatal(err)
	}
	return s, operator, admin, rows
}
func cleanupRequest(row types.VersionData) *types.CleanupVersionReq {
	return &types.CleanupVersionReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedDocumentVersion: 1, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func TestKnowledgeRetentionProtectorsAndAuthorization(t *testing.T) {
	s, operator, admin, rows := retentionFixture(t)
	candidate := rows[0]
	plan, err := GetRetentionPlan(admin, s, candidate.DocumentId)
	if err != nil || len(plan.Items) != 4 || !plan.Items[3].Eligible {
		t.Fatal("candidate", plan, err)
	}
	for _, row := range plan.Items[:3] {
		if !slices.Contains(row.Reasons, "recent") || row.Eligible {
			t.Fatal("latest version not protected")
		}
	}
	assertReason := func(reason string) {
		t.Helper()
		p, e := GetRetentionPlan(admin, s, candidate.DocumentId)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range p.Items {
			if item.VersionId == candidate.Id && slices.Contains(item.Reasons, reason) {
				return
			}
		}
		t.Fatal("missing reason", reason)
	}
	for _, setting := range []struct{ sql, reason string }{
		{"update business.document_versions set created_at=now() where id=$1", "too_young"},
		{"update business.document_versions set created_at=now()-interval '40 days',source_epoch=1 where id=$1", "archive_history"},
		{"update business.document_versions set source_epoch=0,index_status='pending' where id=$1", "index_busy"},
	} {
		if _, err = s.Conn.Exec(setting.sql, candidate.Id); err != nil {
			t.Fatal(err)
		}
		assertReason(setting.reason)
		if _, err = CleanupDocumentVersion(admin, s, cleanupRequest(candidate)); err == nil {
			t.Fatal("protected cleanup accepted", setting.reason)
		}
	}
	if _, err = s.Conn.Exec("update business.document_versions set index_status='ready' where id=$1", candidate.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = GetRetentionPlan(operator, s, candidate.DocumentId); err == nil {
		t.Fatal("operator preview")
	}
	tenant := uuid.NewString()
	if _, err = s.TenantsModel.Insert(admin, &model.Tenants{Id: tenant, Code: "retention-foreign", Name: "Synthetic foreign tenant"}); err != nil {
		t.Fatal(err)
	}
	foreign := *Current(admin).User
	foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), tenant, "retention.foreign"
	if _, err = s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignAdmin, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	if _, err = GetRetentionPlan(foreignAdmin, s, candidate.DocumentId); err == nil {
		t.Fatal("foreign preview")
	}
	if _, err = CleanupDocumentVersion(foreignAdmin, s, cleanupRequest(candidate)); err == nil {
		t.Fatal("foreign admin cleanup")
	}
	if _, err = s.Conn.Exec("update business.outbox_events set status='pending' where topic='knowledge.index' and payload->>'version_id'=$1", candidate.Id); err != nil {
		t.Fatal(err)
	}
	assertReason("index_busy")
	if _, err = CleanupDocumentVersion(admin, s, cleanupRequest(candidate)); err == nil {
		t.Fatal("undelivered index cleanup")
	}
	if _, err = s.Conn.Exec("update business.outbox_events set status='delivered' where topic='knowledge.index' and payload->>'version_id'=$1", candidate.Id); err != nil {
		t.Fatal(err)
	}
	activated, err := ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertReason("active")
	if _, err = CleanupDocumentVersion(admin, s, cleanupRequest(candidate)); err == nil {
		t.Fatal("stale document preview")
	}
	if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: candidate.DocumentId, ExpectedVersion: activated.Version}); err != nil {
		t.Fatal(err)
	}
	run := newTask(t, operator, s, newConversation(t, operator, s).Id)
	if err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		return s.RunEvidenceModel.InsertTx(ctx, tx, &model.RunEvidence{Id: uuid.NewString(), TenantId: Current(operator).User.TenantId, RunId: run.Id, VersionId: candidate.Id, ChunkId: uuid.NewString(), Title: "Synthetic", Excerpt: "Synthetic policy", ContentHash: HashToken("Synthetic policy")})
	}); err != nil {
		t.Fatal(err)
	}
	assertReason("referenced")
	req := cleanupRequest(candidate)
	req.ExpectedDocumentVersion = 3
	if _, err = CleanupDocumentVersion(admin, s, req); err == nil {
		t.Fatal("referenced version cleanup")
	}
}
func TestKnowledgeCleanupDurableReplayAndLeaseFence(t *testing.T) {
	s, operator, admin, rows := retentionFixture(t)
	candidate := rows[0]
	req := cleanupRequest(candidate)
	if _, err := CleanupDocumentVersion(operator, s, req); err == nil {
		t.Fatal("operator cleanup")
	}
	pending, err := CleanupDocumentVersion(admin, s, req)
	if err != nil || pending.CleanupState != versionCleanupPending {
		t.Fatal(err)
	}
	replay, err := CleanupDocumentVersion(admin, s, req)
	if err != nil || replay.Id != pending.Id {
		t.Fatal("replay", err)
	}
	stored, err := s.DocumentVersionsModel.FindOne(admin, candidate.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DownloadOriginalSource(admin, s, &types.VersionPagesReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id}); err == nil {
		t.Fatal("retiring source read")
	}
	if _, err = PreviewVersionPages(admin, s, &types.VersionPagesReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id}); err == nil {
		t.Fatal("retiring pages read")
	}
	if _, err = GetIndexSource(admin, s, &types.IndexSourceReq{VersionId: candidate.Id, JobId: stored.JobId}); err == nil {
		t.Fatal("late source read")
	}
	if _, err = CompleteIndex(admin, s, &types.IndexCompleteReq{VersionId: candidate.Id, JobId: stored.JobId, Status: "ready", ChunkCount: 1, ContentHash: stored.ContentHash}); err == nil {
		t.Fatal("late callback")
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: candidate.DocumentId, VersionId: candidate.Id, ExpectedVersion: 1}); err == nil {
		t.Fatal("retiring activation")
	}
	var event model.OutboxEvents
	if err = s.Conn.QueryRow(&event, "select * from business.outbox_events where aggregate_id=$1 and topic='knowledge.cleanup'", candidate.DocumentId); err != nil {
		t.Fatal(err)
	}
	claim := func() {
		t.Helper()
		if err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
			return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id)
		}); err != nil {
			t.Fatal(err)
		}
		current, e := s.OutboxEventsModel.FindOne(admin, event.Id)
		if e != nil {
			t.Fatal(e)
		}
		event = *current
	}
	claim()
	status := http.StatusServiceUnavailable
	expire := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/internal/v1/knowledge/cleanup" {
			t.Error("wrong target")
		}
		if expire {
			_, e := s.Conn.Exec("update business.outbox_events set lease_expires_at=now()-interval '1 second' where id=$1", event.Id)
			if e != nil {
				t.Error(e)
			}
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-test-token")
	if err = CleanupVersionIndex(admin, s, &event); err == nil {
		t.Fatal("missing index failure")
	}
	status = http.StatusAccepted
	expire = true
	if err = CleanupVersionIndex(admin, s, &event); err == nil {
		t.Fatal("expired owner committed")
	}
	stored, _ = s.DocumentVersionsModel.FindOne(admin, candidate.Id)
	if stored.Content == "" || stored.CleanupState != versionCleanupPending {
		t.Fatal("premature purge")
	}
	expire = false
	claim()
	if err = CleanupVersionIndex(admin, s, &event); err != nil {
		t.Fatal(err)
	}
	if err = CleanupVersionIndex(admin, s, &event); err != nil || calls != 3 {
		t.Fatal("completion replay", err, calls)
	}
	stored, _ = s.DocumentVersionsModel.FindOne(admin, candidate.Id)
	if stored.Content != "" || len(stored.SourceBytes) != 0 || stored.PageTexts != "[]" || stored.CleanupState != versionPurged || stored.SourceSize != candidate.SourceSize {
		t.Fatal("purged data or metadata")
	}
	replay, err = CleanupDocumentVersion(admin, s, req)
	if err != nil || replay.CleanupState != versionPurged {
		t.Fatal("purged replay", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.cleanup' and object_id=$1", candidate.Id); err != nil || count != 1 {
		t.Fatal("audit duplication", count, err)
	}
	if _, err = s.Conn.Exec("update business.users set enabled=false,auth_version=auth_version+1 where id=$1", Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = CleanupDocumentVersion(admin, s, req); err == nil {
		t.Fatal("revoked identity replay")
	}
}
func TestKnowledgeCleanupLateFailureRollsBack(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	req := cleanupRequest(rows[0])
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if _, err := CleanupDocumentVersion(admin, s, req); err == nil {
		t.Fatal("late failure missing")
	}
	s.AuditEventsModel = original
	row, err := s.DocumentVersionsModel.FindOne(admin, rows[0].Id)
	if err != nil || row.CleanupState != versionRetained || row.Content == "" {
		t.Fatal("state rollback", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.outbox_events where aggregate_id=$1 and topic='knowledge.cleanup'", row.DocumentId); err != nil || count != 0 {
		t.Fatal("outbox rollback", err)
	}
	if _, err = CleanupDocumentVersion(admin, s, req); err != nil {
		t.Fatal("retry after rollback", err)
	}
}

func TestKnowledgeCleanupWaitsForEvidenceCommit(t *testing.T) {
	s, operator, admin, rows := retentionFixture(t)
	candidate := rows[0]
	run := newTask(t, operator, s, newConversation(t, operator, s).Id)
	locked, release := make(chan struct{}), make(chan struct{})
	writer := make(chan error, 1)
	go func() {
		writer <- s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
			if _, err := s.DocumentsModel.FindOneForShareTx(ctx, tx, Current(admin).User.TenantId, candidate.DocumentId); err != nil {
				return err
			}
			close(locked)
			<-release
			return s.RunEvidenceModel.InsertTx(ctx, tx, &model.RunEvidence{Id: uuid.NewString(), TenantId: Current(admin).User.TenantId, RunId: run.Id, VersionId: candidate.Id, ChunkId: uuid.NewString(), Title: "Concurrent synthetic evidence", Excerpt: "Synthetic policy", ContentHash: HashToken("Synthetic policy")})
		})
	}()
	select {
	case <-locked:
	case err := <-writer:
		t.Fatal("writer lock", err)
	case <-time.After(5 * time.Second):
		t.Fatal("writer lock timeout")
	}
	finished := make(chan error, 1)
	go func() { _, err := CleanupDocumentVersion(admin, s, cleanupRequest(candidate)); finished <- err }()
	select {
	case err := <-finished:
		close(release)
		<-writer
		t.Fatal("cleanup passed an uncommitted evidence read", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		assertAccountCode(t, err, "VERSION_PROTECTED")
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not resume")
	}
}
