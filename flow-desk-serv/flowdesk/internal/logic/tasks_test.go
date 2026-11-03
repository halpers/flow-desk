package logic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/seed"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func taskContext(t *testing.T, s *svc.ServiceContext) (context.Context, string) {
	t.Helper()
	token, raw, err := Login(context.Background(), s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(context.Background(), s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return WithIdentity(context.Background(), identity), raw
}
func newConversation(t *testing.T, ctx context.Context, s *svc.ServiceContext) types.ConversationData {
	t.Helper()
	row, err := CreateConversation(ctx, s, &types.CreateConversationReq{Title: "Synthetic query", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func newTask(t *testing.T, ctx context.Context, s *svc.ServiceContext, conversation string) types.RunData {
	t.Helper()
	row, err := SendMessage(ctx, s, &types.SendMessageReq{ConversationId: conversation, IdempotencyKey: uuid.NewString(), ClientMessageId: uuid.NewString(), Content: "查询 FD202611020002"})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func leaseTask(t *testing.T, s *svc.ServiceContext, runID string) types.ExecutionContext {
	t.Helper()
	run, err := s.RunsModel.FindOne(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := RenewLease(context.Background(), s, &types.LeaseReq{RunId: runID, DispatchId: run.DispatchId, LeaseGeneration: run.LeaseGeneration})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func eventRequest(runID, kind string, payload map[string]any) *types.AgentEventReq {
	return &types.AgentEventReq{RunId: runID, SourceEventId: uuid.NewString(), LeaseGeneration: 1, Type: kind, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}
func TestMessageIdempotencyAndTransactionalOutbox(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	conversation := newConversation(t, ctx, s)
	request := &types.SendMessageReq{ConversationId: conversation.Id, IdempotencyKey: uuid.NewString(), ClientMessageId: uuid.NewString(), Content: "查询 FD202611020002"}
	first, err := SendMessage(ctx, s, request)
	if err != nil {
		t.Fatal(err)
	}
	same, err := SendMessage(ctx, s, request)
	if err != nil || same.Id != first.Id {
		t.Fatal("same request created another task", err)
	}
	request.IdempotencyKey = uuid.NewString()
	same, err = SendMessage(ctx, s, request)
	if err != nil || same.Id != first.Id {
		t.Fatal("client message replay created another task", err)
	}
	request.Content = "changed"
	if _, err = SendMessage(ctx, s, request); err == nil {
		t.Fatal("changed replay accepted")
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: conversation.Id, Limit: 20})
	if err != nil || len(messages.Items) != 2 {
		t.Fatal("messages not atomic", err)
	}
	events, err := ListRunEvents(ctx, s, first.Id, 0)
	if err != nil || len(events) != 1 || events[0].Type != domain.EventRunQueued {
		t.Fatal("missing queued event", err)
	}
	run, err := s.RunsModel.FindOne(ctx, first.Id)
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := s.OutboxEventsModel.FindOneByDispatchId(ctx, run.DispatchId)
	if err != nil || outbox.Status != "pending" {
		t.Fatal("missing durable command", err)
	}
}
func TestConcurrentMessagesAndActorQueueLimit(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	conversation := newConversation(t, ctx, s)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := SendMessage(ctx, s, &types.SendMessageReq{ConversationId: conversation.Id, IdempotencyKey: uuid.NewString(), ClientMessageId: uuid.NewString(), Content: "query"})
			results <- err
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var problem *BusinessError
			if !errors.As(err, &problem) || problem.Code != "CONVERSATION_BUSY" {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatal("concurrent sends were not serialized")
	}
	second := newConversation(t, ctx, s)
	newTask(t, ctx, s, second.Id)
	third := newConversation(t, ctx, s)
	_, err := SendMessage(ctx, s, &types.SendMessageReq{ConversationId: third.Id, IdempotencyKey: uuid.NewString(), ClientMessageId: uuid.NewString(), Content: "query"})
	var problem *BusinessError
	if !errors.As(err, &problem) || problem.Code != "QUEUE_FULL" {
		t.Fatal("actor quota not enforced", err)
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: third.Id})
	if err != nil || len(messages.Items) != 0 {
		t.Fatal("rejected task left partial messages", err)
	}
}
func TestEventReplaySnapshotAndInvalidCallbackRollback(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	conversation := newConversation(t, ctx, s)
	run := newTask(t, ctx, s, conversation.Id)
	lease := leaseTask(t, s, run.Id)
	allowed := map[string]bool{}
	for _, name := range lease.Tools {
		allowed[name] = true
	}
	if !allowed["ticket.create"] || !allowed["ticket.add_note"] || !allowed[domain.ToolOrderLookup] {
		t.Fatal("lease did not advertise current business tools")
	}
	started := eventRequest(run.Id, domain.EventRunStarted, map[string]any{})
	ack, err := IngestEvent(ctx, s, started, lease.Capability)
	if err != nil || ack.Seq != 2 {
		t.Fatal(err)
	}
	repeated, err := IngestEvent(ctx, s, started, "")
	if err != nil || repeated.Seq != ack.Seq {
		t.Fatal("lost ack could not be recovered", err)
	}
	started.Payload["error_code"] = "CHANGED"
	if _, err = IngestEvent(ctx, s, started, lease.Capability); err == nil {
		t.Fatal("changed event accepted")
	}
	invalid := eventRequest(run.Id, domain.EventMessageCompleted, map[string]any{"message_id": run.CurrentMessage.Id, "content": "answer", "citations": []any{}, "action_receipts": []any{}, "unrecognized": "field"})
	if _, err = IngestEvent(ctx, s, invalid, lease.Capability); err == nil {
		t.Fatal("invalid schema accepted")
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || snapshot.LastEventSeq != 2 || snapshot.CurrentMessage.Status != "streaming" {
		t.Fatal("invalid callback partially committed", err)
	}
	delete(invalid.Payload, "unrecognized")
	invalid.SourceEventId = uuid.NewString()
	if _, err = IngestEvent(ctx, s, invalid, lease.Capability); err != nil {
		t.Fatal(err)
	}
	succeeded := eventRequest(run.Id, domain.EventRunSucceeded, map[string]any{})
	last, err := IngestEvent(ctx, s, succeeded, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = GetRun(ctx, s, run.Id)
	if err != nil || snapshot.Status != domain.RunSucceeded || snapshot.CurrentMessage.Content != "answer" || snapshot.LastEventSeq != last.Seq {
		t.Fatal("inconsistent terminal snapshot", err)
	}
	replay, err := ListRunEvents(ctx, s, run.Id, 2)
	if err != nil || len(replay) != 2 || replay[0].Seq != 3 {
		t.Fatal("replay lost or duplicated events", err)
	}
	if _, err = IngestEvent(ctx, s, succeeded, ""); err != nil {
		t.Fatal("terminal duplicate ack rejected", err)
	}
	if _, err = IngestEvent(ctx, s, eventRequest(run.Id, domain.EventToolStarted, map[string]any{}), lease.Capability); err == nil {
		t.Fatal("terminal run accepted new event")
	}
}
func TestTenantOwnerToolsAndSessionRevocation(t *testing.T) {
	s := testService(t)
	ctx, refreshRaw := taskContext(t, s)
	actor := Current(ctx).User
	if err := seed.Orders(ctx, s, actor.TenantId); err != nil {
		t.Fatal(err)
	}
	otherTenant := uuid.NewString()
	_, err := s.TenantsModel.Insert(ctx, &model.Tenants{Id: otherTenant, Code: "other", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if err = seed.Orders(ctx, s, otherTenant); err != nil {
		t.Fatal(err)
	}
	otherActor := *actor
	otherActor.Id = uuid.NewString()
	otherActor.Account = "other"
	otherActor.TenantId = otherTenant
	if _, err = s.UsersModel.Insert(ctx, &otherActor); err != nil {
		t.Fatal(err)
	}
	otherCtx := WithIdentity(context.Background(), &Identity{User: &otherActor})
	conversation := newConversation(t, ctx, s)
	run := newTask(t, ctx, s, conversation.Id)
	if _, err = GetConversation(otherCtx, s, conversation.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("cross-tenant conversation visible", err)
	}
	if _, err = GetRun(otherCtx, s, run.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("cross-tenant run visible", err)
	}
	ownerCopy := *actor
	ownerCopy.Id = uuid.NewString()
	if _, err = GetConversation(WithIdentity(context.Background(), &Identity{User: &ownerCopy}), s, conversation.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("another owner can read conversation", err)
	}
	orders, err := ListOrders(ctx, s, &types.OrdersReq{Limit: 2})
	if err != nil || len(orders.Items) != 2 || !orders.HasMore {
		t.Fatal(err)
	}
	if _, err = GetOrder(otherCtx, s, orders.Items[0].Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("cross-tenant order visible", err)
	}
	if _, err = ListOrders(otherCtx, s, &types.OrdersReq{Cursor: orders.NextCursor}); err == nil {
		t.Fatal("cross-tenant cursor accepted")
	}
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(ctx, s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	result, err := CallTool(ctx, s, &types.ToolReq{ToolName: domain.ToolOrderLookup, Arguments: map[string]any{"order_no": "FD202611020002"}}, lease.Capability)
	if err != nil || result.Order.Id != orders.Items[1].Id || result.Order.TotalAmount != "102.90" {
		t.Fatal("tool read wrong tenant or lost decimal precision", err)
	}
	if _, _, err = Refresh(ctx, s, refreshRaw); err != nil {
		t.Fatal(err)
	}
	leaseTask(t, s, run.Id) // A rotation preserves the initiating session family.
	if err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		return s.UsersModel.SetEnabledTx(ctx, tx, actor.Id, false)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = CallTool(ctx, s, &types.ToolReq{ToolName: domain.ToolOrderLookup, Arguments: map[string]any{"order_no": "FD202611020002"}}, lease.Capability); err == nil {
		t.Fatal("revoked user retained tool authority")
	}
}
