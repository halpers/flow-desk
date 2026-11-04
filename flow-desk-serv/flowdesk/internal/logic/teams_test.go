package logic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/seed"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTeam(t *testing.T, s *svc.ServiceContext, admin context.Context) types.TeamData {
	t.Helper()
	row, err := CreateTeam(admin, s, &types.CreateTeamReq{Name: "Synthetic team " + uuid.NewString()[:8], IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func memberChange(t *testing.T, s *svc.ServiceContext, admin context.Context, team types.TeamData, user string, member bool) types.TeamData {
	t.Helper()
	row, err := UpdateTeamMembership(admin, s, &types.TeamMembershipReq{TeamId: team.Id, UserId: user, Member: member, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func manualTicket(t *testing.T, s *svc.ServiceContext, ctx context.Context) (types.TicketData, *types.CreateTicketReq) {
	t.Helper()
	if err := seed.Orders(context.Background(), s, Current(ctx).User.TenantId); err != nil {
		t.Fatal(err)
	}
	orders, err := ListOrders(ctx, s, &types.OrdersReq{OrderNo: "FD202611020002", Limit: 1})
	if err != nil || len(orders.Items) != 1 {
		t.Fatal(err)
	}
	req := &types.CreateTicketReq{OrderId: orders.Items[0].Id, ExpectedVersion: orders.Items[0].Version, Title: "Team synthetic ticket", Description: "SYNTHETIC_TEAM_PRIVATE", Category: "logistics", Priority: "normal", IdempotencyKey: uuid.NewString()}
	row, err := CreateTicket(ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	return row, req
}
func TestTeamTicketRevocationBlocksActionsReplayAndHistory(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	team := newTeam(t, s, admin)
	if _, err := CreateTeam(ctx, s, &types.CreateTeamReq{Name: "forbidden", IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("operator managed teams")
	}
	if _, err := ListTeamUsers(ctx, s); err == nil {
		t.Fatal("operator read directory")
	}
	ticket, create := manualTicket(t, s, ctx)
	ticket, err := SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: team.Id, ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GetTicket(ctx, s, ticket.Id); err != errMissing {
		t.Fatal("non-member detail", err)
	}
	listed, err := ListTickets(ctx, s, &types.TicketsReq{Limit: 20})
	if err != nil || len(listed.Items) != 0 {
		t.Fatal("non-member list", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	replay := &types.TeamMembershipReq{TeamId: team.Id, UserId: Current(ctx).User.Id, Member: true, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()}
	team, err = UpdateTeamMembership(admin, s, replay)
	if err != nil {
		t.Fatal(err)
	}
	note := &types.AddNoteReq{TicketId: ticket.Id, ExpectedVersion: ticket.Version, Content: "Synthetic initial note", IdempotencyKey: uuid.NewString()}
	ticket, err = AddTicketNote(ctx, s, note)
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	found, err := CallTool(context.Background(), s, &types.ToolReq{ToolName: ticketSearchTool, Arguments: map[string]any{"order_no": "FD202611020002"}}, lease.Capability)
	if err != nil || len(found.Tickets) != 1 {
		t.Fatal("team search", err)
	}
	prepare := &types.PrepareActionReq{RunId: run.Id, ActionId: uuid.NewString(), ToolName: ticketNoteTool, Arguments: map[string]any{"ticket_id": ticket.Id, "expected_version": ticket.Version, "content": "MUST_NOT_WRITE"}, EvidenceIds: []string{}}
	approval, err := PrepareAction(context.Background(), s, prepare, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	waited := waitApproval(t, s, ctx, run, lease, approval)
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
	if _, err = DecideApproval(ctx, s, &types.DecisionReq{ApprovalId: approval.Id, ExpectedVersion: waited.Approvals[0].Version, Decision: "approve", IdempotencyKey: uuid.NewString()}); err != errMissing {
		t.Fatal("revoked member approved", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	lease = approveTicket(t, s, ctx, run, approval)
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
	if _, err = CreateTicket(ctx, s, create); err != errMissing {
		t.Fatal("create replay leaked", err)
	}
	if _, err = AddTicketNote(ctx, s, note); err != errMissing {
		t.Fatal("note replay leaked", err)
	}
	if _, err = PrepareAction(context.Background(), s, prepare, lease.Capability); err != errMissing {
		t.Fatal("prepare replay leaked", err)
	}
	if _, err = ExecuteAction(context.Background(), s, &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}, lease.Capability); err != errMissing {
		t.Fatal("revoked action executed", err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || snapshot.CurrentMessage.Content != resourceHiddenMessage || len(snapshot.Approvals) != 0 || len(snapshot.ActionReceipts) != 0 {
		t.Fatal("snapshot", err)
	}
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: run.ConversationId, Limit: 20})
	if err != nil || messages.Items[len(messages.Items)-1].Content != resourceHiddenMessage {
		t.Fatal("history", err)
	}
	events, err := ListRunEvents(ctx, s, run.Id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != "resource.revoked" || strings.Contains(marshal(event), "MUST_NOT_WRITE") {
			t.Fatal("SSE leak")
		}
		var frame any
		_ = json.Unmarshal([]byte(marshal(event)), &frame)
		if err = domain.ValidateEvent(frame); err != nil {
			t.Fatal("redaction contract", err)
		}
	}
	persisted, err := s.ActionsModel.FindOne(context.Background(), approval.ActionId)
	if err != nil || persisted.Status != stateInvalidated {
		t.Fatal("action not invalidated", err)
	}
	// A cached old grant response never applies membership again.
	if _, err = UpdateTeamMembership(admin, s, replay); err != nil {
		t.Fatal(err)
	}
	if _, err = GetTicket(ctx, s, ticket.Id); err != errMissing {
		t.Fatal("grant replay restored member", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	if _, err = GetTicket(ctx, s, ticket.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = SetTicketTeam(ctx, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: "", ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("member changed scope")
	}
}

func TestTeamKnowledgeFiltersAndRechecksEvidence(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	team := newTeam(t, s, admin)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Team policy", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	base, err = SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	content := "Synthetic team policy valid for seven days."
	version, err := UploadDocument(admin, s, &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Team policy", Filename: "policy.md", Content: content, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if _, err = CompleteIndex(context.Background(), s, &types.IndexCompleteReq{VersionId: raw.Id, JobId: raw.JobId, ContentHash: raw.ContentHash, ChunkCount: 1, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: raw.DocumentId, VersionId: raw.Id, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	bases, err := ListKnowledgeBases(ctx, s)
	if err != nil || len(bases.Items) != 0 {
		t.Fatal("base list", err)
	}
	if _, err = ListDocuments(ctx, s, base.Id); err != errMissing {
		t.Fatal("non-member document", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	scope, err := GetKnowledgeScope(context.Background(), s, run.Id, lease.Capability)
	if err != nil || len(scope.Versions) != 1 {
		t.Fatal("member scope", err)
	}
	checked, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{{VersionId: version.Id, ChunkId: uuid.NewString(), Excerpt: content, ContentHash: HashToken(content)}}}, lease.Capability)
	if err != nil || len(checked.Items) != 1 {
		t.Fatal("evidence", err)
	}
	citation := map[string]any{"id": checked.Items[0].Id, "version_id": version.Id, "chunk_id": checked.Items[0].ChunkId, "title": "Team policy"}
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventMessageCompleted, map[string]any{"message_id": run.CurrentMessage.Id, "content": content, "citations": []any{citation}, "action_receipts": []any{}}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
	scope, err = GetKnowledgeScope(context.Background(), s, run.Id, lease.Capability)
	if err != nil || len(scope.Versions) != 0 {
		t.Fatal("revoked scope", err)
	}
	if _, err = GetCitation(ctx, s, checked.Items[0].Id); err == nil {
		t.Fatal("revoked citation")
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || strings.Contains(snapshot.CurrentMessage.Content, content) {
		t.Fatal("revoked policy snapshot", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: base.AclRevision}); err != nil {
		t.Fatal(err)
	}
	scope, err = GetKnowledgeScope(context.Background(), s, run.Id, lease.Capability)
	if err != nil || len(scope.Versions) != 0 {
		t.Fatal("membership bypassed role ACL", err)
	}
}

func TestTeamTenantBoundaryAndReceiptRevocation(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	team := newTeam(t, s, admin)
	tenant := uuid.NewString()
	if _, err := s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: tenant, Code: "foreign", Name: "Foreign synthetic tenant"}); err != nil {
		t.Fatal(err)
	}
	foreign := *Current(ctx).User
	foreign.Id = uuid.NewString()
	foreign.Account = "foreign.operator"
	foreign.TenantId = tenant
	if _, err := s.UsersModel.Insert(context.Background(), &foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateTeamMembership(admin, s, &types.TeamMembershipReq{TeamId: team.Id, UserId: foreign.Id, Member: true, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()}); err != errMissing {
		t.Fatal("foreign member", err)
	}
	foreignAdmin := WithIdentity(context.Background(), &Identity{User: &model.Users{Id: foreign.Id, TenantId: tenant, Role: "admin", Enabled: true}})
	foreignTeam := newTeam(t, s, foreignAdmin)
	run, old, approval := preparedTicket(t, s, ctx)
	waitApproval(t, s, ctx, run, old, approval)
	lease := approveTicket(t, s, ctx, run, approval)
	request := &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}
	receipt, err := ExecuteAction(context.Background(), s, request, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: receipt.BusinessObjectId, TeamId: foreignTeam.Id, ExpectedVersion: 1, IdempotencyKey: uuid.NewString()}); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("foreign scope", err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, true)
	if _, err = SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: receipt.BusinessObjectId, TeamId: team.Id, ExpectedVersion: 1, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	team = memberChange(t, s, admin, team, Current(ctx).User.Id, false)
	if _, err = GetActionReceipt(context.Background(), s, request, lease.Capability); err != errMissing {
		t.Fatal("old receipt leaked", err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || len(snapshot.ActionReceipts) != 0 || len(snapshot.CurrentMessage.ActionReceipts) != 0 {
		t.Fatal("public receipts leaked", err)
	}
	// The immutable receipt remains in storage for auditing and authorized recovery.
	stored, err := s.ActionReceiptsModel.FindOneByTenantIdActionId(context.Background(), Current(ctx).User.TenantId, approval.ActionId)
	if err != nil || marshal(receiptData(stored)) != marshal(receipt) {
		t.Fatal("receipt changed", err)
	}
}

func TestTeamMembershipFenceConflictAndRollback(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	team := newTeam(t, s, admin)
	var group sync.WaitGroup
	outcomes := make(chan error, 2)
	for range 2 {
		group.Go(func() {
			_, err := UpdateTeamMembership(admin, s, &types.TeamMembershipReq{TeamId: team.Id, UserId: Current(ctx).User.Id, Member: true, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()})
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
			if !errors.As(err, &business) || business.Code != "VERSION_CONFLICT" {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatal("concurrent versions", successes)
	}
	team.Revision++
	entered := make(chan struct{})
	release := make(chan struct{})
	reader := make(chan error, 1)
	go func() {
		reader <- s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
			if err := teamAccessTx(ctx, s, tx, Current(admin).User, sql.NullString{String: team.Id, Valid: true}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	writer := make(chan error, 1)
	go func() {
		_, err := UpdateTeamMembership(admin, s, &types.TeamMembershipReq{TeamId: team.Id, UserId: Current(ctx).User.Id, Member: false, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()})
		writer <- err
	}()
	select {
	case err := <-writer:
		close(release)
		t.Fatal("revocation passed live authorization lock", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-reader; err != nil {
		t.Fatal(err)
	}
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	team.Revision++
	ticket, _ := manualTicket(t, s, ctx)
	exclusive := make(chan struct{})
	unlock := make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		owner <- s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
			if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, Current(admin).User.TenantId); err != nil {
				return err
			}
			close(exclusive)
			<-unlock
			return nil
		})
	}()
	<-exclusive
	_, busyErr := GetTicket(ctx, s, ticket.Id)
	close(unlock)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(busyErr, model.ErrAuthorizationBusy) {
		t.Fatal("authorization read did not fail fast", busyErr)
	}
	if _, err := s.Conn.Exec(`CREATE FUNCTION reject_team_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='team.membership' THEN RAISE EXCEPTION 'synthetic rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_team_audit BEFORE INSERT ON business.audit_events FOR EACH ROW EXECUTE FUNCTION reject_team_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateTeamMembership(admin, s, &types.TeamMembershipReq{TeamId: team.Id, UserId: Current(ctx).User.Id, Member: true, ExpectedRevision: team.Revision, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("audit failure committed")
	}
	row, err := s.TeamsModel.FindOneByTenantIdId(context.Background(), Current(ctx).User.TenantId, team.Id)
	if err != nil || row.Revision != team.Revision {
		t.Fatal("revision rollback", err)
	}
	var exists bool
	userID := Current(ctx).User.Id
	if err = s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		var err error
		exists, err = s.TeamMembersModel.ExistsMembershipTx(ctx, tx, Current(admin).User.TenantId, team.Id, userID)
		return err
	}); err != nil || exists {
		t.Fatal("membership rollback", err)
	}
}

func TestLegacyTicketSearchHistoryIsConservativelyProtected(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	team := newTeam(t, s, admin)
	ticket, _ := manualTicket(t, s, ctx)
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunStarted, map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventToolStarted, map[string]any{"tool_call_id": uuid.NewString(), "tool_name": ticketSearchTool, "summary": "Synthetic legacy search"}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "2026110402_team_history.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err = s.Conn.Exec(up); err != nil {
		t.Fatal(err)
	}
	if _, err = SetTicketTeam(admin, s, &types.TicketTeamReq{TicketId: ticket.Id, TeamId: team.Id, ExpectedVersion: ticket.Version, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || snapshot.CurrentMessage.Content != resourceHiddenMessage {
		t.Fatal("legacy run leaked", err)
	}
}
