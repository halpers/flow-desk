package logic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/domain"
	"flowdesk/internal/seed"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func preparedTicket(t *testing.T, s *svc.ServiceContext, ctx context.Context, evidenceSetup ...func(types.RunData, types.ExecutionContext) []string) (types.RunData, types.ExecutionContext, types.ApprovalData) {
	t.Helper()
	if err := seed.Orders(context.Background(), s, Current(ctx).User.TenantId); err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	orders, err := ListOrders(ctx, s, &types.OrdersReq{OrderNo: "FD202611020002", Limit: 1})
	if err != nil || len(orders.Items) != 1 {
		t.Fatal("order", err)
	}
	order := orders.Items[0]
	req := &types.PrepareActionReq{RunId: run.Id, ActionId: uuid.NewString(), ToolName: ticketCreateTool, Arguments: map[string]any{"order_id": order.Id, "expected_version": order.Version, "title": "Synthetic delivery follow-up", "description": "Only synthetic data", "category": "after_sales", "priority": "normal"}, EvidenceIds: []string{}}
	if len(evidenceSetup) > 0 {
		req.EvidenceIds = evidenceSetup[0](run, lease)
	}
	approval, err := PrepareAction(context.Background(), s, req, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	same, err := PrepareAction(context.Background(), s, req, lease.Capability)
	if err != nil || marshal(same) != marshal(approval) {
		t.Fatal("prepare replay", err)
	}
	req.Arguments["title"] = "changed"
	if _, err = PrepareAction(context.Background(), s, req, lease.Capability); err == nil {
		t.Fatal("changed proposal accepted")
	}
	return run, lease, approval
}
func waitApproval(t *testing.T, s *svc.ServiceContext, ctx context.Context, run types.RunData, lease types.ExecutionContext, approval types.ApprovalData) types.RunData {
	t.Helper()
	req := eventRequest(run.Id, domain.EventRunWaitingApproval, map[string]any{"action_id": approval.ActionId, "checkpoint_ref": "persisted-checkpoint"})
	if _, err := IngestEvent(context.Background(), s, req, lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestEvent(context.Background(), s, req, ""); err != nil {
		t.Fatal("lost wait ack replay", err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || snapshot.Status != domain.RunWaitingApproval || len(snapshot.Approvals) != 1 {
		t.Fatal("wait snapshot", err)
	}
	return snapshot
}
func approveTicket(t *testing.T, s *svc.ServiceContext, ctx context.Context, run types.RunData, approval types.ApprovalData) types.ExecutionContext {
	t.Helper()
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	req := &types.DecisionReq{ApprovalId: approval.Id, ExpectedVersion: snapshot.Approvals[0].Version, Decision: "approve", IdempotencyKey: uuid.NewString()}
	queued, err := DecideApproval(ctx, s, req)
	if err != nil || queued.Status != domain.RunQueued {
		t.Fatal("approve", err)
	}
	repeated, err := DecideApproval(ctx, s, req)
	if err != nil || repeated.Version != queued.Version {
		t.Fatal("decision lost response", err)
	}
	lease := leaseTask(t, s, run.Id)
	event := eventRequest(run.Id, domain.EventRunStarted, map[string]any{})
	event.LeaseGeneration = lease.LeaseGeneration
	if _, err = IngestEvent(context.Background(), s, event, lease.Capability); err != nil {
		t.Fatal(err)
	}
	return lease
}
func TestActionApprovalReceiptReplayAndSummaryFailure(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run, old, approval := preparedTicket(t, s, ctx)
	actionReq := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	if _, err := ExecuteAction(context.Background(), s, actionReq, old.Capability); err == nil {
		t.Fatal("unapproved write")
	}
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	if _, err := ExecuteAction(context.Background(), s, actionReq, old.Capability); err == nil {
		t.Fatal("old generation wrote")
	}
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunFailed, map[string]any{"error_code": "STALE_WORKER"}), old.Capability); err == nil {
		t.Fatal("old callback changed resumed run")
	}
	receipt, err := ExecuteAction(context.Background(), s, actionReq, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate losing the successful response and replaying the node after restart.
	repeated, err := ExecuteAction(context.Background(), s, actionReq, lease.Capability)
	if err != nil || marshal(repeated) != marshal(receipt) {
		t.Fatal("receipt replay", err)
	}
	rows, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 100})
	if err != nil || len(rows.Items) != 1 {
		t.Fatal("duplicate ticket", err)
	}
	failed := eventRequest(run.Id, domain.EventRunFailed, map[string]any{"error_code": "MODEL_OUTPUT_INVALID"})
	failed.LeaseGeneration = lease.LeaseGeneration
	if _, err = IngestEvent(context.Background(), s, failed, lease.Capability); err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || snapshot.Status != domain.RunFailed || len(snapshot.ActionReceipts) != 1 || len(snapshot.CurrentMessage.ActionReceipts) != 1 {
		t.Fatal("summary failure hid committed receipt", err)
	}
	if _, err = RetryRun(ctx, s, retryRequest(snapshot)); err == nil {
		t.Fatal("committed business effect allowed a fresh retry")
	} else {
		operationError(t, err, "RETRY_REQUIRES_REVIEW")
	}
	// Immutable snapshot is enforced by PostgreSQL, including accidental generated CRUD.
	action, err := s.ActionsModel.FindOne(context.Background(), approval.ActionId)
	if err != nil {
		t.Fatal(err)
	}
	action.Arguments = "{}"
	if err = s.ActionsModel.Update(context.Background(), action); err == nil {
		t.Fatal("mutable prepared action")
	}
}
func TestCancelDecisionRaceAndCommittedEffects(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run, old, approval := preparedTicket(t, s, ctx)
	snapshot := waitApproval(t, s, ctx, run, old, approval)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, _ = DecideApproval(ctx, s, &types.DecisionReq{ApprovalId: approval.Id, ExpectedVersion: snapshot.Approvals[0].Version, Decision: "approve", IdempotencyKey: uuid.NewString()})
	}()
	go func() {
		defer group.Done()
		<-start
		_, _ = CancelRun(ctx, s, &types.CancelRunReq{RunId: run.Id, ExpectedVersion: snapshot.Version, IdempotencyKey: uuid.NewString()})
	}()
	close(start)
	group.Wait()
	latest, err := GetRun(ctx, s, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	// If approval won first, the old cancel version conflicted; retry explicitly on fresh snapshot.
	if !domain.IsTerminal(latest.Status) {
		latest, err = CancelRun(ctx, s, &types.CancelRunReq{RunId: run.Id, ExpectedVersion: latest.Version, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
	}
	if latest.Status != domain.RunCancelled {
		t.Fatal("cancel state", latest.Status)
	}
	if _, err = ExecuteAction(context.Background(), s, &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}, old.Capability); err == nil {
		t.Fatal("cancelled effect")
	}
	rows, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 100})
	if err != nil || len(rows.Items) != 0 {
		t.Fatal("cancel race wrote", err)
	}
}
func TestActionResourceChangeAndTransactionRollback(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run, old, approval := preparedTicket(t, s, ctx)
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	// A late audit error must roll back ticket, event, receipt and action transition together.
	if _, err := s.Conn.ExecCtx(context.Background(), "alter table business.audit_events add constraint fail_audit check(operation<>'ticket.created')"); err != nil {
		t.Fatal(err)
	}
	req := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	if _, err := ExecuteAction(context.Background(), s, req, lease.Capability); err == nil {
		t.Fatal("late failure ignored")
	}
	rows, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 100})
	if err != nil || len(rows.Items) != 0 {
		t.Fatal("partial commit", err)
	}
	if _, err = s.Conn.ExecCtx(context.Background(), "alter table business.audit_events drop constraint fail_audit"); err != nil {
		t.Fatal(err)
	}
	action, err := s.ActionsModel.FindOne(context.Background(), approval.ActionId)
	if err != nil {
		t.Fatal(err)
	}
	orderID := jsonObject(action.Arguments)["order_id"]
	if _, err = s.Conn.ExecCtx(context.Background(), "update business.orders set version=version+1 where id=$1", orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = ExecuteAction(context.Background(), s, req, lease.Capability); err == nil {
		t.Fatal("stale resource wrote")
	}
	changed, err := s.ActionsModel.FindOne(context.Background(), approval.ActionId)
	if err != nil || changed.Status != "invalidated" {
		t.Fatal("proposal not invalidated", err)
	}
}
func TestPendingInputRecoveryAndExpiry(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	event := eventRequest(run.Id, domain.EventRunWaitingInput, map[string]any{"input_id": id, "question": "Synthetic order number?", "checkpoint_ref": "persisted"})
	if _, err := IngestEvent(context.Background(), s, event, lease.Capability); err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || len(snapshot.PendingInputs) != 1 {
		t.Fatal("missing input", err)
	}
	req := &types.SubmitInputReq{RunId: run.Id, InputId: id, ExpectedVersion: snapshot.Version, Content: "FD202611020002", IdempotencyKey: uuid.NewString()}
	queued, err := SubmitInput(ctx, s, req)
	if err != nil || queued.Status != domain.RunQueued {
		t.Fatal("input resume", err)
	}
	again, err := SubmitInput(ctx, s, req)
	if err != nil || again.Version != queued.Version {
		t.Fatal("input replay", err)
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: run.ConversationId, Limit: 100})
	if err != nil || len(messages.Items) != 3 || messages.Items[1].Role != "user" || messages.Items[2].Role != "assistant" {
		t.Fatal("input/message order", err)
	}
	current := leaseTask(t, s, run.Id)
	if current.LeaseGeneration != 2 || current.ResumeData["input"] != req.Content {
		t.Fatal("resume data", current.ResumeData)
	}
	if _, err = RenewLease(context.Background(), s, &types.LeaseReq{RunId: run.Id, DispatchId: uuid.NewString(), LeaseGeneration: 1}); err == nil {
		t.Fatal("stale renewal")
	}
	if _, err = s.Conn.ExecCtx(context.Background(), "update business.runs set deadline_at=$2 where id=$1", run.Id, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = ExpireOverdueRuns(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	final, err := GetRun(ctx, s, run.Id)
	if err != nil || final.Status != domain.RunExpired {
		t.Fatal("expiry", err)
	}
}
func TestTicketPermissionsNotesAndTenantBoundary(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run, old, approval := preparedTicket(t, s, ctx)
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	receipt, err := ExecuteAction(context.Background(), s, &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	id := receipt.BusinessObjectId
	req := &types.AddNoteReq{TicketId: id, ExpectedVersion: 1, Content: "Synthetic note", IdempotencyKey: uuid.NewString()}
	ticket, err := AddTicketNote(ctx, s, req)
	if err != nil || ticket.Version != 2 {
		t.Fatal("note", err)
	}
	same, err := AddTicketNote(ctx, s, req)
	if err != nil || same.Version != 2 {
		t.Fatal("note replay", err)
	}
	if _, err = ChangeTicketStatus(ctx, s, &types.TicketChangeReq{TicketId: id, ExpectedVersion: 2, Status: "closed", IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("operator manages ticket")
	}
	admin := knowledgeAdminContext(t, s, ctx)
	closed, err := ChangeTicketStatus(admin, s, &types.TicketChangeReq{TicketId: id, ExpectedVersion: 2, Status: "closed", IdempotencyKey: uuid.NewString()})
	if err != nil || closed.Version != 3 {
		t.Fatal(err)
	}
	if _, err = AddTicketNote(ctx, s, &types.AddNoteReq{TicketId: id, ExpectedVersion: 3, Content: "must reject", IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("closed note")
	}
	audit, err := ListAuditEvents(admin, s, &types.PageReq{Limit: 100})
	if err != nil || len(audit.Items) != 3 {
		t.Fatal("audit", err, len(audit.Items))
	}
	foreign := *Current(ctx)
	foreign.User = &model.Users{Id: uuid.NewString(), TenantId: uuid.NewString(), Role: "operator", Enabled: true}
	if _, err = GetTicket(WithIdentity(context.Background(), &foreign), s, id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
}

func TestCancellationPreservesCommittedReceipt(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run, old, approval := preparedTicket(t, s, ctx)
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	req := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	receipt, err := ExecuteAction(context.Background(), s, req, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	final, err := CancelRun(ctx, s, &types.CancelRunReq{RunId: run.Id, ExpectedVersion: snapshot.Version, IdempotencyKey: uuid.NewString()})
	if err != nil || final.Status != domain.RunCancelled || len(final.ActionReceipts) != 1 || len(final.CurrentMessage.ActionReceipts) != 1 {
		t.Fatal("cancel hid committed effect", err)
	}
	replay, err := ExecuteAction(context.Background(), s, req, lease.Capability)
	if err != nil || marshal(replay) != marshal(receipt) {
		t.Fatal("committed receipt unreadable", err)
	}
	rows, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 100})
	if err != nil || len(rows.Items) != 1 {
		t.Fatal("cancelled receipt replay duplicated", err)
	}
}
func TestPolicyAndPermissionRevocationBeforeExecution(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic action policy", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	content := "合成售后规则：配送问题可登记工单。"
	version, err := UploadDocument(admin, s, &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Synthetic policy", Filename: "policy.txt", Content: content, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompleteIndex(context.Background(), s, &types.IndexCompleteReq{VersionId: version.Id, JobId: raw.JobId, ContentHash: raw.ContentHash, Status: "ready", ChunkCount: 1}); err != nil {
		t.Fatal(err)
	}
	document, err := ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: version.DocumentId, VersionId: version.Id, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	run, old, approval := preparedTicket(t, s, ctx, func(run types.RunData, lease types.ExecutionContext) []string {
		evidence, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{{VersionId: version.Id, ChunkId: uuid.NewString(), Excerpt: content, ContentHash: HashToken(content)}}}, lease.Capability)
		if err != nil {
			t.Fatal(err)
		}
		return []string{evidence.Items[0].Id}
	})
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	actor := *Current(ctx).User
	actor.Enabled = false
	if err = s.UsersModel.Update(context.Background(), &actor); err != nil {
		t.Fatal(err)
	}
	req := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	if _, err = ExecuteAction(context.Background(), s, req, lease.Capability); err == nil {
		t.Fatal("disabled actor wrote")
	}
	actor.Enabled = true
	if err = s.UsersModel.Update(context.Background(), &actor); err != nil {
		t.Fatal(err)
	}
	if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: document.Id, ExpectedVersion: document.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = ExecuteAction(context.Background(), s, req, lease.Capability); err == nil {
		t.Fatal("withdrawn policy authorized effect")
	}
	var business *BusinessError
	if !errors.As(err, &business) || business.Code != "EVIDENCE_REVOKED" {
		t.Fatal("policy error", err)
	}
	action, err := s.ActionsModel.FindOne(context.Background(), approval.ActionId)
	if err != nil || action.Status != "invalidated" {
		t.Fatal("policy did not invalidate action", err)
	}
	rows, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 100})
	if err != nil || len(rows.Items) != 0 {
		t.Fatal("unauthorized effect", err)
	}
}

func TestOutboxLostAcceptanceResponseReusesDispatch(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	var mutex sync.Mutex
	seen := map[string]int{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mutex.Lock()
		seen[string(body)]++
		requests++
		first := requests == 1
		mutex.Unlock()
		if first {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-service-credential")
	if attempted, err := DispatchNext(context.Background(), s); err != nil || !attempted {
		t.Fatal("first dispatch", err)
	}
	row, err := s.RunsModel.FindOne(context.Background(), run.Id)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.OutboxEventsModel.FindOneByDispatchId(context.Background(), row.DispatchId)
	if err != nil || pending.Status != "pending" || pending.AttemptCount != 1 {
		t.Fatal("retry not durable", err)
	}
	if _, err = s.Conn.ExecCtx(context.Background(), "update business.outbox_events set available_at=now() where id=$1", pending.Id); err != nil {
		t.Fatal(err)
	}
	if attempted, err := DispatchNext(context.Background(), s); err != nil || !attempted {
		t.Fatal("replayed dispatch", err)
	}
	delivered, err := s.OutboxEventsModel.FindOneByDispatchId(context.Background(), row.DispatchId)
	if err != nil || delivered.Status != "delivered" || delivered.AttemptCount != 2 {
		t.Fatal("delivery status", err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(seen) != 1 || requests != 2 {
		t.Fatal("dispatch payload changed", seen)
	}
}
