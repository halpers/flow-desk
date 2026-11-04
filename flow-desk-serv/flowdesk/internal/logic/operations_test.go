package logic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func failOperation(t *testing.T, s *svc.ServiceContext, ctx context.Context, id string) types.RunData {
	t.Helper()
	run, err := GetRun(ctx, s, id)
	if err != nil {
		t.Fatal(err)
	}
	lease := leaseTask(t, s, id)
	if run.Status == domain.RunQueued {
		if _, err = IngestEvent(ctx, s, eventRequest(id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = IngestEvent(ctx, s, eventRequest(id, domain.EventRunFailed, map[string]any{"error_code": "EXECUTION_FAILED"}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	run, err = GetRun(ctx, s, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func retryRequest(run types.RunData) *types.RetryRunReq {
	return &types.RetryRunReq{RunId: run.Id, ExpectedVersion: run.Version, IdempotencyKey: uuid.NewString()}
}
func operationActor(t *testing.T, s *svc.ServiceContext, ctx context.Context, role, tenant string) context.Context {
	t.Helper()
	row := *Current(ctx).User
	row.Id = uuid.NewString()
	row.Account = uuid.NewString()
	row.Role = role
	row.TenantId = tenant
	if _, err := s.UsersModel.Insert(ctx, &row); err != nil {
		t.Fatal(err)
	}
	token, _, err := Login(ctx, s, &types.LoginReq{Account: row.Account, Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(ctx, s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return WithIdentity(context.Background(), identity)
}
func operationError(t *testing.T, err error, code string) {
	t.Helper()
	var problem *BusinessError
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("want %s got %v", code, err)
	}
}

func TestRunOperationsPaginationOwnerTenantAndCursor(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	tenant := Current(ctx).User.TenantId
	first := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	second := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	page, err := ListRuns(ctx, s, &types.RunsPageReq{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Id != second.Id || !page.HasMore {
		t.Fatal(page, err)
	}
	// Mutable status timestamps must not disturb the cursor based on creation time.
	if _, err = s.Conn.Exec("update business.runs set updated_at=now()+interval '1 day' where id=$1", first.Id); err != nil {
		t.Fatal(err)
	}
	next, err := ListRuns(ctx, s, &types.RunsPageReq{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].Id != first.Id || next.HasMore {
		t.Fatal(next, err)
	}
	if _, err = ListRuns(ctx, s, &types.RunsPageReq{Status: "failed", Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor crossed filters")
	}
	if _, err = ListRuns(ctx, s, &types.RunsPageReq{Scope: "tenant"}); err != errForbidden {
		t.Fatal("operator tenant scope", err)
	}
	peer := operationActor(t, s, ctx, "operator", tenant)
	peerPage, err := ListRuns(peer, s, &types.RunsPageReq{})
	if err != nil || len(peerPage.Items) != 0 {
		t.Fatal("peer listed messages", err)
	}
	if _, err = GetRunTrace(peer, s, first.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("peer trace", err)
	}
	supervisor := operationActor(t, s, ctx, "supervisor", tenant)
	all, err := ListRuns(supervisor, s, &types.RunsPageReq{Scope: "tenant", Status: "failed"})
	if err != nil || len(all.Items) != 2 || all.Items[0].CanRetry {
		t.Fatal(all, err)
	}
	if _, err = RetryRun(supervisor, s, retryRequest(first)); err != errMissing {
		t.Fatal("supervisor retried peer", err)
	}
	foreignTenant := uuid.NewString()
	if _, err = s.TenantsModel.Insert(ctx, &model.Tenants{Id: foreignTenant, Code: uuid.NewString(), Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	foreign := operationActor(t, s, ctx, "admin", foreignTenant)
	other, err := ListRuns(foreign, s, &types.RunsPageReq{Scope: "tenant", RunId: first.Id})
	if err != nil || len(other.Items) != 0 {
		t.Fatal("cross tenant list", err)
	}
	if _, err = GetRunTrace(foreign, s, first.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("cross tenant trace", err)
	}
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), "FD202611020002") || strings.Contains(string(raw), "content") {
		t.Fatal("metadata exposed input")
	}
}

func TestRunTraceUnavailableAndTeamRevocationDuringAgentCall(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	older := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	ticket, _ := manualTicket(t, s, ctx)
	team := newTeam(t, s, admin)
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	var err error
	ticket, err = SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: team.Id, ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	if err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, Current(ctx).User.TenantId, run.Id)
		if err != nil {
			return err
		}
		return rememberTicketTx(ctx, s, tx, row, ticket.Id)
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	revoke := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer synthetic-token" || r.URL.Query().Get("tenant_id") != Current(ctx).User.TenantId {
			t.Error("missing trusted trace identity")
		}
		if !revoke {
			w.WriteHeader(503)
			return
		}
		team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
		_, _ = w.Write([]byte(`{"available":false,"truncated":false,"nodes":[],"models":[],"tools":[],"usage":{"cost_status":"unknown"}}`))
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-token")
	_, err = GetRunTrace(ctx, s, run.Id)
	operationError(t, err, "OBSERVATION_UNAVAILABLE")
	revoke = true
	if _, err = GetRunTrace(ctx, s, run.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("stale authorization returned trace", err)
	}
	if _, err = GetRunTrace(ctx, s, run.Id); !errors.Is(err, model.ErrNotFound) || calls != 2 {
		t.Fatal("hidden trace called agent", err, calls)
	}
	page, err := ListRuns(ctx, s, &types.RunsPageReq{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Id != older.Id || page.HasMore {
		t.Fatal("filter after pagination", err)
	}
	page, err = ListRuns(admin, s, &types.RunsPageReq{Scope: "tenant"})
	if err != nil || len(page.Items) != 2 {
		t.Fatal("admin viewer incorrectly used owner's memberships", err)
	}
	supervisor := operationActor(t, s, ctx, "supervisor", Current(ctx).User.TenantId)
	roleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(supervisor).User.Id); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"available":false,"truncated":false,"nodes":[],"models":[],"tools":[],"usage":{"cost_status":"unknown"}}`))
	}))
	defer roleServer.Close()
	s.AgentClient = client.NewAgentClient(roleServer.URL, "synthetic-token")
	if _, err = GetRunTrace(supervisor, s, older.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("stale supervisor permission returned peer trace", err)
	}
}

func TestExpiredRunRetryReplayRechecksCurrentTeam(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	ticket, _ := manualTicket(t, s, ctx)
	team := newTeam(t, s, admin)
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	var err error
	ticket, err = SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: team.Id, ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	if err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, Current(ctx).User.TenantId, run.Id)
		if err != nil {
			return err
		}
		return rememberTicketTx(ctx, s, tx, row, ticket.Id)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.runs set deadline_at=now()-interval '1 second' where id=$1", run.Id); err != nil {
		t.Fatal(err)
	}
	if err = ExpireOverdueRuns(ctx, s); err != nil {
		t.Fatal(err)
	}
	run, err = GetRun(ctx, s, run.Id)
	if err != nil || run.Status != domain.RunExpired {
		t.Fatal(err)
	}
	req := retryRequest(run)
	child, err := RetryRun(ctx, s, req)
	if err != nil || child.RetryOfRunId != run.Id {
		t.Fatal(err)
	}
	memberChange(t, s, admin, team, Current(ctx).User.Id, false)
	if _, err = RetryRun(ctx, s, req); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("idempotent retry bypassed revoked source", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.runs where retry_of_run_id=$1", run.Id); err != nil || count != 1 {
		t.Fatal("revoked replay changed child count", err)
	}
}

func TestRunRetryConcurrentReplayDepthAndCurrentSession(t *testing.T) {
	s := testService(t)
	ctx, raw := taskContext(t, s)
	parent := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	// A new login may retry work from a revoked old session.
	if err := Logout(ctx, s, raw); err != nil {
		t.Fatal(err)
	}
	ctx, _ = taskContext(t, s)
	request := retryRequest(parent)
	var group sync.WaitGroup
	results := make(chan types.RunData, 3)
	failures := make(chan error, 3)
	for index := range 3 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			req := *request
			if index == 2 {
				req.IdempotencyKey = uuid.NewString()
			}
			row, err := RetryRun(ctx, s, &req)
			results <- row
			failures <- err
		}(index)
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	childID := ""
	for row := range results {
		if childID != "" && row.Id != childID {
			t.Fatal("duplicate child")
		}
		childID = row.Id
		if row.RetryOfRunId != parent.Id || row.RetryDepth != 1 {
			t.Fatal("missing provenance", row)
		}
	}
	child, err := s.RunsModel.FindOne(ctx, childID)
	if err != nil || child.SessionId != Current(ctx).Session.Id {
		t.Fatal("old login reused", err)
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: parent.ConversationId})
	if err != nil || len(messages.Items) != 4 {
		t.Fatal("duplicate retry messages", err)
	}
	for depth := int64(2); depth <= 3; depth++ {
		failed := failOperation(t, s, ctx, childID)
		next, err := RetryRun(ctx, s, retryRequest(failed))
		if err != nil || next.RetryDepth != depth {
			t.Fatal(depth, next, err)
		}
		childID = next.Id
	}
	last := failOperation(t, s, ctx, childID)
	_, err = RetryRun(ctx, s, retryRequest(last))
	operationError(t, err, "RETRY_LIMIT")
	original, err := GetRun(ctx, s, parent.Id)
	if err != nil || original.Status != domain.RunFailed || original.Version != parent.Version {
		t.Fatal("old terminal mutated", err)
	}
}

func TestRunRetryRejectsActionsInputsBusyQuotaAndStaleVersion(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	prepared, _, _ := preparedTicket(t, s, ctx)
	prepared = failOperation(t, s, ctx, prepared.Id)
	_, err := RetryRun(ctx, s, retryRequest(prepared))
	operationError(t, err, "RETRY_REQUIRES_REVIEW")
	parent := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	req := retryRequest(parent)
	req.ExpectedVersion--
	_, err = RetryRun(ctx, s, req)
	operationError(t, err, "VERSION_CONFLICT")
	active := newTask(t, ctx, s, parent.ConversationId)
	_, err = RetryRun(ctx, s, retryRequest(parent))
	operationError(t, err, "CONVERSATION_BUSY")
	second := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	// Two other active conversations exhaust the actor quota for a retry.
	quotaParent := failOperation(t, s, ctx, active.Id)
	newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	_, err = RetryRun(ctx, s, retryRequest(quotaParent))
	operationError(t, err, "QUEUE_FULL")
	failOperation(t, s, ctx, second.Id)
	if _, err = s.Conn.Exec("update business.runs set input_count=1 where id=$1", parent.Id); err != nil {
		t.Fatal(err)
	}
	_, err = RetryRun(ctx, s, retryRequest(parent))
	operationError(t, err, "RETRY_REQUIRES_REVIEW")
}

func TestRunRetryRollsBackWhenAuditFails(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	parent := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	if _, err := s.Conn.Exec(`CREATE FUNCTION reject_retry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='run.retry' THEN RAISE EXCEPTION 'synthetic retry rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_retry BEFORE INSERT ON business.audit_events FOR EACH ROW EXECUTE FUNCTION reject_retry()`); err != nil {
		t.Fatal(err)
	}
	req := retryRequest(parent)
	if _, err := RetryRun(ctx, s, req); err == nil {
		t.Fatal("failed audit accepted")
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: parent.ConversationId})
	if err != nil || len(messages.Items) != 2 {
		t.Fatal("retry messages survived rollback", err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.runs where retry_of_run_id=$1", parent.Id); err != nil || count != 0 {
		t.Fatal("child survived rollback", err)
	}
	if err = s.Conn.QueryRow(&count, "select count(*) from business.outbox_events"); err != nil || count != 1 {
		t.Fatal("retry outbox survived rollback", err)
	}
	if _, err = s.Conn.Exec("DROP TRIGGER reject_retry ON business.audit_events"); err != nil {
		t.Fatal(err)
	}
	if _, err = RetryRun(ctx, s, req); err != nil {
		t.Fatal("rolled back key could not retry", err)
	}
}
