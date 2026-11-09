package logic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/domain"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func lifecycleAgentFixture() types.AgentLifecycleData {
	return types.AgentLifecycleData{Available: true, ObservationOnly: true, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ProtectionReasons: []string{}, CommandCount: 1, CheckpointCount: 2, CheckpointBlobCount: 3, CheckpointWriteCount: 1}
}

func TestRunLifecycleProtectionMatrix(t *testing.T) {
	run := types.RunOperationData{Status: domain.RunSucceeded}
	inventory := model.RunLifecycleInventory{OldEnough: true, ObservedAt: time.Now()}
	agent := lifecycleAgentFixture()
	if got := buildRunLifecycle(run, inventory, agent); !got.ArchiveCandidate || !got.ObservationOnly || got.MinimumAgeDays != 30 {
		t.Fatal("baseline not planning candidate")
	}
	tests := []struct {
		Name, Reason string
		Change       func(*types.RunOperationData, *model.RunLifecycleInventory, *types.AgentLifecycleData)
	}{
		{"queued", "RUN_NOT_TERMINAL", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			r.Status = domain.RunQueued
		}},
		{"running", "RUN_NOT_TERMINAL", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			r.Status = domain.RunRunning
		}},
		{"input", "RUN_NOT_TERMINAL", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			r.Status = domain.RunWaitingInput
		}},
		{"approval", "RUN_NOT_TERMINAL", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			r.Status = domain.RunWaitingApproval
		}},
		{"cancelling", "RUN_NOT_TERMINAL", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			r.Status = domain.RunCancelling
		}},
		{"age", "MINIMUM_AGE_NOT_REACHED", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.OldEnough = false
		}},
		{"lease", "BUSINESS_LEASE_ACTIVE", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.LeaseActive = true
		}},
		{"conversation", "CONVERSATION_ACTIVE", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.ActiveConversationRunCount = 1
		}},
		{"dispatch", "DISPATCH_UNSETTLED", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.UnsettledDispatchCount = 1
		}},
		{"approval-pending", "APPROVAL_UNSETTLED", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.PendingApprovalCount = 1
		}},
		{"input-pending", "INPUT_UNSETTLED", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.PendingInputCount = 1
		}},
		{"actions", "BUSINESS_REFERENCES", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.ActionCount = 1
		}},
		{"receipts", "BUSINESS_REFERENCES", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.ReceiptCount = 1
		}},
		{"resources", "BUSINESS_REFERENCES", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.ResourceReferenceCount = 1
		}},
		{"evidence", "EVIDENCE_REFERENCES", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.EvidenceCount = 1
		}},
		{"retry", "RETRY_CHAIN_REFERENCES", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			b.RetryLinkCount = 1
		}},
		{"scope", "AGENT_SCOPE_UNAVAILABLE", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			a.Available = false
			a.ProtectionReasons = []string{"AGENT_SCOPE_UNAVAILABLE"}
		}},
		{"callback", "CALLBACK_UNACKNOWLEDGED", func(r *types.RunOperationData, b *model.RunLifecycleInventory, a *types.AgentLifecycleData) {
			a.ProtectionReasons = []string{"CALLBACK_UNACKNOWLEDGED"}
		}},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			r, b, a := run, inventory, agent
			test.Change(&r, &b, &a)
			got := buildRunLifecycle(r, b, a)
			if got.ArchiveCandidate || !slices.Contains(got.ProtectionReasons, test.Reason) {
				t.Fatalf("missing protection: %s", test.Reason)
			}
		})
	}
	for _, status := range []string{domain.RunSucceeded, domain.RunFailed, domain.RunCancelled, domain.RunExpired} {
		r := run
		r.Status = status
		if !buildRunLifecycle(r, inventory, agent).ArchiveCandidate {
			t.Fatalf("terminal %s blocked", status)
		}
	}
}

func TestRunLifecycleRealInventoryAndNoMutations(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/internal/v1/runs/"+run.Id+"/lifecycle" || r.URL.Query().Get("tenant_id") != Current(ctx).User.TenantId || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("untrusted lifecycle request")
		}
		_ = json.NewEncoder(w).Encode(lifecycleAgentFixture())
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-token")
	before, err := s.RunsModel.FindOne(ctx, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := GetRunLifecycle(ctx, s, run.Id)
	if err != nil || preview.ArchiveCandidate || preview.Business.MessageCount != 2 || preview.Business.EventCount < 2 || preview.Business.DispatchCount != 1 || preview.Business.UnsettledDispatchCount != 1 || preview.Agent.CheckpointCount != 2 {
		t.Fatal(preview, err)
	}
	after, err := s.RunsModel.FindOne(ctx, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	if *before != *after {
		t.Fatal("read changed run")
	}
	if !slices.Contains(preview.ProtectionReasons, "MINIMUM_AGE_NOT_REACHED") || !slices.Contains(preview.ProtectionReasons, "DISPATCH_UNSETTLED") {
		t.Fatal("missing observed protectors")
	}
	raw, _ := json.Marshal(preview)
	if strings.Contains(string(raw), "FD202611020002") || strings.Contains(string(raw), "content") || strings.Contains(string(raw), "arguments") {
		t.Fatal("preview leaked body")
	}
	if _, err = s.Conn.Exec("update business.outbox_events set status='delivered',lease_expires_at=null where aggregate_id=$1", run.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.runs set updated_at=now()-interval '31 days',lease_expires_at=null where id=$1", run.Id); err != nil {
		t.Fatal(err)
	}
	preview, err = GetRunLifecycle(ctx, s, run.Id)
	if err != nil || !preview.ArchiveCandidate || len(preview.ProtectionReasons) != 0 || !preview.ObservationOnly {
		t.Fatal(preview, err)
	}
	// A newer run in the same conversation protects the old task, as does a retry link.
	child := newTask(t, ctx, s, run.ConversationId)
	if _, err = s.Conn.Exec("update business.runs set retry_of_run_id=$1 where id=$2", run.Id, child.Id); err != nil {
		t.Fatal(err)
	}
	preview, err = GetRunLifecycle(ctx, s, run.Id)
	if err != nil || preview.ArchiveCandidate || preview.Business.RetryLinkCount != 1 || preview.Business.ActiveConversationRunCount != 1 {
		t.Fatal(preview, err)
	}
	if calls != 3 {
		t.Fatal("unexpected observation attempts")
	}
}

func TestRunLifecycleAuthorizationAndUnavailableDoNotExposePartialData(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	peer := operationActor(t, s, ctx, "operator", Current(ctx).User.TenantId)
	supervisor := operationActor(t, s, ctx, "supervisor", Current(ctx).User.TenantId)
	calls := 0
	mode := "unavailable"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if mode == "unavailable" {
			w.WriteHeader(503)
			return
		}
		if mode == "revoke" {
			_, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(supervisor).User.Id)
			if err != nil {
				t.Error(err)
			}
		}
		if mode == "version" {
			_, err := s.Conn.Exec("update business.runs set version=version+1 where id=$1", run.Id)
			if err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(lifecycleAgentFixture())
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-token")
	if _, err := GetRunLifecycle(peer, s, run.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("peer observed unrelated task", err)
	}
	if calls != 0 {
		t.Fatal("unauthorized request reached Agent")
	}
	data, err := GetRunLifecycle(ctx, s, run.Id)
	operationError(t, err, "LIFECYCLE_UNAVAILABLE")
	if data.Run.Id != "" || data.Business.MessageCount != 0 || data.Agent.Available {
		t.Fatal("partial unavailable response")
	}
	mode = "healthy"
	if _, err = GetRunLifecycle(supervisor, s, run.Id); err != nil {
		t.Fatal(err)
	}
	mode = "revoke"
	if _, err = GetRunLifecycle(supervisor, s, run.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("stale supervisor metadata", err)
	}
	mode = "version"
	data, err = GetRunLifecycle(ctx, s, run.Id)
	operationError(t, err, "LIFECYCLE_CHANGED")
	if data.Run.Id != "" {
		t.Fatal("changed preview leaked partial metadata")
	}
	if _, err = GetRunLifecycle(ctx, s, "invalid"); err != errArgument {
		t.Fatal("invalid id accepted", err)
	}
	if _, err = GetRunLifecycle(context.Background(), s, uuid.NewString()); err != ErrUnauthorized {
		t.Fatal("missing identity", err)
	}
}

func TestRunLifecycleCurrentTeamAndSessionAfterObservation(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	ticket, _ := manualTicket(t, s, ctx)
	team := newTeam(t, s, admin)
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	ticket, err := SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: team.Id, ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, Current(ctx).User.TenantId, run.Id)
		if err != nil {
			return err
		}
		return rememberTicketTx(ctx, s, tx, row, ticket.Id)
	})
	if err != nil {
		t.Fatal(err)
	}
	revokeTeam := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revokeTeam {
			team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
		} else {
			_, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(ctx).Session.Id)
			if err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(lifecycleAgentFixture())
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-token")
	_, err = GetRunLifecycle(ctx, s, run.Id)
	if err != ErrUnauthorized {
		t.Fatal("revoked session observed lifecycle", err)
	}
	// The prior owner still owns the run; use its freshly authenticated session.
	token, _, err := Login(admin, s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(admin, s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithIdentity(context.Background(), identity)
	revokeTeam = true
	data, err := GetRunLifecycle(ctx, s, run.Id)
	if !errors.Is(err, model.ErrNotFound) || data.Run.Id != "" {
		t.Fatal("team revoked during call returned metadata", err)
	}
}
