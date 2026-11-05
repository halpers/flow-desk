package logic

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func policyRequest(base string, revision, keep, days int64) *types.UpdateRetentionPolicyReq {
	return &types.UpdateRetentionPolicyReq{KnowledgeBaseId: base, ExpectedRevision: revision, KeepLatest: keep, MinimumAgeDays: days, IdempotencyKey: uuid.NewString()}
}
func TestRetentionPolicyDefaultsRangesAndLegacyMigration(t *testing.T) {
	tenant, base := uuid.NewString(), uuid.NewString()
	s := testService(t, func(db *sql.DB) {
		if _, err := db.Exec("insert into tenants(id,code,name)values($1,'policy-legacy','Synthetic legacy')", tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("insert into knowledge_bases(id,tenant_id,name)values($1,$2,'Synthetic legacy')", base, tenant); err != nil {
			t.Fatal(err)
		}
	})
	legacy, err := s.KnowledgeBasesModel.FindOneByTenantIdId(context.Background(), tenant, base)
	if err != nil || legacy.RetentionKeepLatest != 3 || legacy.RetentionMinimumAgeDays != 30 || legacy.RetentionRevision != 1 {
		t.Fatal("legacy defaults", legacy, err)
	}
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	created, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic policy defaults", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := GetRetentionPolicy(admin, s, created.Id)
	if err != nil || policy.KeepLatest != 3 || policy.MinimumAgeDays != 30 || policy.Revision != 1 {
		t.Fatal("new defaults", policy, err)
	}
	for _, limits := range [][2]int64{{0, 30}, {101, 30}, {3, 0}, {3, 3651}} {
		if _, err = UpdateRetentionPolicy(admin, s, policyRequest(created.Id, 1, limits[0], limits[1])); err == nil {
			t.Fatal("invalid policy", limits)
		}
	}
	for index, limits := range [][2]int64{{1, 1}, {100, 3650}} {
		if _, err = UpdateRetentionPolicy(admin, s, policyRequest(created.Id, int64(index+1), limits[0], limits[1])); err != nil {
			t.Fatal("valid boundary", limits, err)
		}
	}
	if _, err = GetRetentionPolicy(operator, s, created.Id); err == nil {
		t.Fatal("operator read")
	}
	if _, err = UpdateRetentionPolicy(operator, s, policyRequest(created.Id, 3, 3, 30)); err == nil {
		t.Fatal("operator write")
	}
	if _, err = GetRetentionPolicy(admin, s, base); err == nil {
		t.Fatal("cross tenant read")
	}
	if _, err = UpdateRetentionPolicy(admin, s, policyRequest(base, 1, 1, 1)); err == nil {
		t.Fatal("cross tenant write")
	}
}
func TestRetentionPolicyReplayRollbackAndStaleCleanup(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	doc, err := s.DocumentsModel.FindOne(admin, rows[0].DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	req := policyRequest(doc.KnowledgeBaseId, 1, 4, 90)
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if _, err = UpdateRetentionPolicy(admin, s, req); err == nil {
		t.Fatal("expected late failure")
	}
	s.AuditEventsModel = original
	policy, err := GetRetentionPolicy(admin, s, doc.KnowledgeBaseId)
	if err != nil || policy.Revision != 1 || policy.KeepLatest != 3 {
		t.Fatal("policy rollback", policy, err)
	}
	updated, err := UpdateRetentionPolicy(admin, s, req)
	if err != nil || updated.Revision != 2 {
		t.Fatal(err)
	}
	if _, err = UpdateRetentionPolicy(admin, s, policyRequest(doc.KnowledgeBaseId, 1, 1, 1)); err == nil {
		t.Fatal("stale policy update")
	}
	stale := cleanupRequest(rows[0])
	stale.ExpectedPolicyRevision = 1
	_, err = CleanupDocumentVersion(admin, s, stale)
	assertAccountCode(t, err, "RETENTION_POLICY_CHANGED")
	stale.ExpectedPolicyRevision = 0
	_, err = CleanupDocumentVersion(admin, s, stale)
	assertAccountCode(t, err, "RETENTION_POLICY_CHANGED")
	stale.ExpectedPolicyRevision = 2
	_, err = CleanupDocumentVersion(admin, s, stale)
	assertAccountCode(t, err, "VERSION_PROTECTED")
	if _, err = UpdateRetentionPolicy(admin, s, policyRequest(doc.KnowledgeBaseId, 2, 1, 1)); err != nil {
		t.Fatal(err)
	}
	replay, err := UpdateRetentionPolicy(admin, s, req)
	if err != nil || replay != updated {
		t.Fatal("immutable write receipt", replay, err)
	}
	current, err := GetRetentionPolicy(admin, s, doc.KnowledgeBaseId)
	if err != nil || current.Revision != 3 || current.KeepLatest != 1 {
		t.Fatal("replay reverted policy", current, err)
	}
	changed := *req
	changed.KeepLatest = 2
	_, err = UpdateRetentionPolicy(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	plan, err := GetRetentionPlan(admin, s, doc.Id)
	if err != nil || plan.PolicyRevision != 3 || plan.KeepLatest != 1 || plan.MinimumAgeDays != 1 {
		t.Fatal("policy preview", plan, err)
	}
	stale.ExpectedPolicyRevision = 3
	if _, err = CleanupDocumentVersion(admin, s, stale); err != nil {
		t.Fatal(err)
	}
	// Already confirmed cleanup survives a later stricter policy.
	if _, err = UpdateRetentionPolicy(admin, s, policyRequest(doc.KnowledgeBaseId, 3, 100, 3650)); err != nil {
		t.Fatal(err)
	}
	replayed, err := CleanupDocumentVersion(admin, s, stale)
	if err != nil || replayed.CleanupState != versionCleanupPending {
		t.Fatal("accepted cleanup replay", err)
	}
	var event model.OutboxEvents
	if err = s.Conn.QueryRow(&event, "select * from business.outbox_events where topic='knowledge.cleanup' and aggregate_id=$1", doc.Id); err != nil {
		t.Fatal(err)
	}
	if err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		return s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id)
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.OutboxEventsModel.FindOne(admin, event.Id)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-policy-test")
	if err = CleanupVersionIndex(admin, s, claimed); err != nil {
		t.Fatal(err)
	}
	version, err := s.DocumentVersionsModel.FindOne(admin, rows[0].Id)
	if err != nil || version.CleanupState != versionPurged || version.Content != "" {
		t.Fatal("confirmed cleanup recovery", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.retention_policy' and object_id=$1", doc.KnowledgeBaseId); err != nil || count != 3 {
		t.Fatal("audit replay count", count, err)
	}
}
func TestRetentionPolicyPreservesLegacyCleanupIdempotency(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	req := cleanupRequest(rows[0])
	if _, err := CleanupDocumentVersion(admin, s, req); err != nil {
		t.Fatal(err)
	}
	// The pre-M7.9 wire digest omitted the new policy field entirely.
	legacyJSON := fmt.Sprintf(`{"DocumentId":%q,"VersionId":%q,"expected_document_version":1,"confirm":true,"IdempotencyKey":%q}`, req.DocumentId, req.VersionId, req.IdempotencyKey)
	var requestHash string
	if err := s.Conn.QueryRow(&requestHash, "select request_hash from business.api_idempotency where operation='knowledge.cleanup' and key_hash=$1", HashToken(req.IdempotencyKey)); err != nil || requestHash != HashToken(legacyJSON) {
		t.Fatal("legacy request digest changed", err)
	}
	doc, err := s.DocumentsModel.FindOne(admin, req.DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateRetentionPolicy(admin, s, policyRequest(doc.KnowledgeBaseId, 1, 100, 3650)); err != nil {
		t.Fatal(err)
	}
	replayed, err := CleanupDocumentVersion(admin, s, req)
	if err != nil || replayed.CleanupState != versionCleanupPending {
		t.Fatal("legacy accepted replay rejected", err)
	}
}

func TestRetentionPolicyManagementScopeAndRevokedReplay(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Retention delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	account := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	base, err := CreateKnowledgeBase(manager, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic scoped policy", ReadRoles: []string{"operator", "admin"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	req := policyRequest(base.Id, 1, 2, 20)
	if _, err = UpdateRetentionPolicy(manager, s, req); err != nil {
		t.Fatal(err)
	}
	team := newTeam(t, s, admin)
	base, err = SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GetRetentionPolicy(manager, s, base.Id); err != errMissing {
		t.Fatal("team scope read", err)
	}
	if _, err = UpdateRetentionPolicy(manager, s, req); err != errMissing {
		t.Fatal("team scope replay", err)
	}
	memberChange(t, s, admin, team, account.Id, true)
	if _, err = UpdateRetentionPolicy(manager, s, req); err != nil {
		t.Fatal("authorized replay", err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: base.AclRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateRetentionPolicy(manager, s, req); err != errMissing {
		t.Fatal("ACL replay", err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin", "operator"}, ExpectedRevision: base.AclRevision + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(manager).Session.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateRetentionPolicy(manager, s, req); err == nil {
		t.Fatal("revoked session replay")
	}
}

type pausedRetentionAudit struct {
	model.AuditEventsModel
	entered, release chan struct{}
}

func (m pausedRetentionAudit) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if row.Operation == "knowledge.retention_policy" {
		close(m.entered)
		<-m.release
	}
	return m.AuditEventsModel.InsertTx(ctx, tx, row)
}
func TestRetentionPolicyUpdateSerializesWithCleanup(t *testing.T) {
	s, _, admin, rows := retentionFixture(t)
	doc, err := s.DocumentsModel.FindOne(admin, rows[0].DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	shadow := *s
	shadow.AuditEventsModel = pausedRetentionAudit{s.AuditEventsModel, entered, release}
	updated := make(chan error, 1)
	go func() {
		_, e := UpdateRetentionPolicy(admin, &shadow, policyRequest(doc.KnowledgeBaseId, 1, 4, 90))
		updated <- e
	}()
	select {
	case <-entered:
	case e := <-updated:
		t.Fatal("update failed", e)
	case <-time.After(5 * time.Second):
		t.Fatal("update lock timeout")
	}
	finished := make(chan error, 1)
	req := cleanupRequest(rows[0])
	req.ExpectedPolicyRevision = 1
	go func() { _, e := CleanupDocumentVersion(admin, s, req); finished <- e }()
	select {
	case e := <-finished:
		close(release)
		<-updated
		t.Fatal("cleanup crossed uncommitted policy", e)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err = <-updated; err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		assertAccountCode(t, err, "RETENTION_POLICY_CHANGED")
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not resume")
	}
	row, err := s.DocumentVersionsModel.FindOne(admin, rows[0].Id)
	if err != nil || row.CleanupState != versionRetained {
		t.Fatal("stale policy cleared data", err)
	}
}
