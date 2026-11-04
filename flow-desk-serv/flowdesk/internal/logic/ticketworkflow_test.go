package logic

import (
	"context"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"sync"
	"testing"
)

func preparedWorkflow(t *testing.T, s *svc.ServiceContext, ctx context.Context, ticket types.TicketData, tool string) (types.RunData, types.ExecutionContext, types.ApprovalData) {
	t.Helper()
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	proposal := &types.PrepareActionReq{RunId: run.Id, ActionId: uuid.NewString(), ToolName: tool, Arguments: map[string]any{"ticket_id": ticket.Id, "expected_version": ticket.Version, "reason": "Synthetic workflow conclusion"}, EvidenceIds: []string{}}
	approval, err := PrepareAction(context.Background(), s, proposal, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if approval.DisplaySnapshot["from_status"] != ticket.Status {
		t.Fatal("snapshot omitted original state")
	}
	waitApproval(t, s, ctx, run, lease, approval)
	lease = approveTicket(t, s, ctx, run, approval)
	return run, lease, approval
}
func completeWorkflowRun(t *testing.T, s *svc.ServiceContext, run types.RunData, lease types.ExecutionContext, receipt types.ReceiptData) {
	t.Helper()
	for _, kind := range []string{domain.EventMessageCompleted, domain.EventRunSucceeded} {
		payload := map[string]any{}
		if kind == domain.EventMessageCompleted {
			payload = map[string]any{"message_id": lease.MessageId, "content": "Synthetic workflow completed with receipt", "citations": []any{}, "action_receipts": []any{map[string]any{"action_id": receipt.ActionId}}}
		}
		event := eventRequest(run.Id, kind, payload)
		event.LeaseGeneration = lease.LeaseGeneration
		if _, err := IngestEvent(context.Background(), s, event, lease.Capability); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTicketProcessingAndResolutionReturnOneImmutableReceipt(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	ticket, _ := manualTicket(t, s, admin)
	for _, tool := range []string{ticketStartTool, ticketResolveTool} {
		run, lease, approval := preparedWorkflow(t, s, admin, ticket, tool)
		request := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
		receipt, err := ExecuteAction(context.Background(), s, request, lease.Capability)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err = GetTicket(admin, s, ticket.Id)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Result["status"] != ticket.Status || ticket.AssigneeUserId != Current(admin).User.Id {
			t.Fatal("workflow state/actor mismatch")
		}
		completeWorkflowRun(t, s, run, lease, receipt)
		replay, err := ExecuteAction(context.Background(), s, request, lease.Capability)
		if err != nil || marshal(replay) != marshal(receipt) {
			t.Fatal("immutable replay", err)
		}
	}
	if ticket.Status != stateResolved || ticket.Version != 3 {
		t.Fatal("unexpected final version/state")
	}
	events, err := ListTicketEvents(admin, s, &types.TimelineReq{TicketId: ticket.Id, Limit: 100})
	if err != nil || len(events.Items) != 3 {
		t.Fatal("duplicate workflow effects", err)
	}
	var notes int64
	if err = s.Conn.QueryRowCtx(context.Background(), &notes, "select count(*) from business.ticket_notes where ticket_id=$1", ticket.Id); err != nil || notes != 1 {
		t.Fatal("resolution note not exactly once", err)
	}
}
func TestTicketResolutionRollsBackNoteStateAndReceipt(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	ticket, _ := manualTicket(t, s, admin)
	ticket, err := ChangeTicketStatus(admin, s, &types.TicketChangeReq{TicketId: ticket.Id, ExpectedVersion: ticket.Version, Status: stateInProgress, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run, lease, approval := preparedWorkflow(t, s, admin, ticket, ticketResolveTool)
	if _, err = s.Conn.Exec("alter table business.audit_events add constraint fail_resolution check(operation<>'ticket.resolved')"); err != nil {
		t.Fatal(err)
	}
	request := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	if _, err = ExecuteAction(context.Background(), s, request, lease.Capability); err == nil {
		t.Fatal("audit failure committed")
	}
	current, err := GetTicket(admin, s, ticket.Id)
	if err != nil || current.Status != stateInProgress || current.Version != ticket.Version {
		t.Fatal("partial state commit", err)
	}
	var notes int64
	if err = s.Conn.QueryRowCtx(context.Background(), &notes, "select count(*) from business.ticket_notes where ticket_id=$1", ticket.Id); err != nil || notes != 0 {
		t.Fatal("partial note commit", err)
	}
	if _, err = s.ActionReceiptsModel.FindOneByTenantIdActionId(context.Background(), Current(admin).User.TenantId, approval.ActionId); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("partial receipt", err)
	}
	if _, err = s.Conn.Exec("alter table business.audit_events drop constraint fail_resolution"); err != nil {
		t.Fatal(err)
	}
	receipt, err := ExecuteAction(context.Background(), s, request, lease.Capability)
	if err != nil || receipt.Result["status"] != stateResolved {
		t.Fatal("same action retry", err)
	}
}
func TestTicketWorkflowPermissionVersionAndCompetingActions(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	ticket, _ := manualTicket(t, s, admin)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAction(context.Background(), s, &types.PrepareActionReq{RunId: run.Id, ActionId: uuid.NewString(), ToolName: ticketStartTool, Arguments: map[string]any{"ticket_id": ticket.Id, "expected_version": ticket.Version, "reason": "forbidden"}, EvidenceIds: []string{}}, lease.Capability); err != errForbidden {
		t.Fatal("operator managed ticket", err)
	}
	if _, err := PrepareAction(context.Background(), s, &types.PrepareActionReq{RunId: run.Id, ActionId: uuid.NewString(), ToolName: ticketStartTool, Arguments: map[string]any{"ticket_id": ticket.Id, "expected_version": ticket.Version, "reason": "forbidden", "assignee_user_id": Current(admin).User.Id}, EvidenceIds: []string{}}, lease.Capability); err != errArgument {
		t.Fatal("model selected actor", err)
	}
	// Two approved plans reference the same version. Only one can begin handling.
	first, one, a := preparedWorkflow(t, s, admin, ticket, ticketStartTool)
	second, two, b := preparedWorkflow(t, s, admin, ticket, ticketStartTool)
	var group sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, pair := range []struct {
		run      types.RunData
		lease    types.ExecutionContext
		approval types.ApprovalData
	}{{first, one, a}, {second, two, b}} {
		group.Go(func() {
			_, err := ExecuteAction(context.Background(), s, &types.ActionReq{RunId: pair.run.Id, ActionId: pair.approval.ActionId}, pair.lease.Capability)
			outcomes <- err
		})
	}
	group.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else {
			var business *BusinessError
			if !errors.As(err, &business) || business.Code != "RESOURCE_CHANGED" {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatal("competing approvals", successes)
	}
	current, err := GetTicket(admin, s, ticket.Id)
	if err != nil || current.Version != 2 || current.Status != stateInProgress {
		t.Fatal("duplicate processing", err)
	}
}
func TestResumeKeepsOriginalGraphVersion(t *testing.T) {
	for _, version := range []string{"read-v1", "ticket-v1", "ticket-v2"} {
		t.Run(version, func(t *testing.T) {
			s := testService(t)
			ctx, _ := taskContext(t, s)
			run, old, approval := preparedTicket(t, s, ctx)
			if _, err := s.Conn.ExecCtx(context.Background(), "update business.outbox_events set payload=jsonb_set(payload,'{graph_version}',to_jsonb($2::text)) where aggregate_id=$1 and topic='run.execute'", run.Id, version); err != nil {
				t.Fatal(err)
			}
			waitApproval(t, s, ctx, run, old, approval)
			_ = approveTicket(t, s, ctx, run, approval)
			var stored string
			if err := s.Conn.QueryRowCtx(context.Background(), &stored, "select payload->>'graph_version' from business.outbox_events where aggregate_id=$1 and topic='run.resume'", run.Id); err != nil || stored != version {
				t.Fatal("resume changed graph", stored, err)
			}
		})
	}
}
