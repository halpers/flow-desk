package logic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func agedHistoryRun(t *testing.T, s *svc.ServiceContext, ctx context.Context) types.RunData {
	t.Helper()
	run := failOperation(t, s, ctx, newTask(t, ctx, s, newConversation(t, ctx, s).Id).Id)
	if _, err := s.Conn.Exec("update business.outbox_events set status='delivered',lease_expires_at=null where aggregate_id=$1", run.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.Exec("update business.runs set updated_at=now()-interval '31 days',lease_expires_at=null where id=$1", run.Id); err != nil {
		t.Fatal(err)
	}
	return run
}
func historyRequest(run types.RunData, generation int64) *types.RunHistoryChangeReq {
	return &types.RunHistoryChangeReq{RunId: run.Id, ExpectedVersion: run.Version, ExpectedGeneration: generation, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func historyAgent(t *testing.T, s *svc.ServiceContext, change func(http.ResponseWriter, *http.Request, client.AgentHistoryReceipt) bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/lifecycle") {
			_ = json.NewEncoder(w).Encode(lifecycleAgentFixture())
			return
		}
		var command struct {
			OperationID, TenantID, Target string
			Generation                    int64
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		_ = json.Unmarshal(raw["operation_id"], &command.OperationID)
		_ = json.Unmarshal(raw["tenant_id"], &command.TenantID)
		_ = json.Unmarshal(raw["target"], &command.Target)
		_ = json.Unmarshal(raw["generation"], &command.Generation)
		receipt := client.AgentHistoryReceipt{OperationId: command.OperationID, TenantId: command.TenantID, RunId: strings.Split(r.URL.Path, "/")[4], Generation: command.Generation, Target: command.Target, CheckpointCount: 2, CheckpointBlobCount: 3, CheckpointWriteCount: 1}
		if change != nil && change(w, r, receipt) {
			return
		}
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	s.AgentClient = client.NewAgentClient(server.URL, "synthetic-token")
	t.Cleanup(server.Close)
	return server
}
func historySnapshots(t *testing.T, s *svc.ServiceContext, ctx context.Context, run types.RunData) (types.MessagePage, []types.EventData) {
	t.Helper()
	messages, err := ListMessages(ctx, s, &types.MessagesReq{ConversationId: run.ConversationId, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	events, err := ListRunEvents(ctx, s, run.Id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return messages, events
}
func historyPayloadCounts(t *testing.T, s *svc.ServiceContext, id string) (int64, int64, int64) {
	t.Helper()
	var row struct {
		Messages int64 `db:"messages"`
		Events   int64 `db:"events"`
		Hot      int64 `db:"hot"`
	}
	err := s.Conn.QueryRow(&row, "select (select count(*) from business.message_payload_archives where run_id=$1) messages,(select count(*) from business.run_event_payload_archives where run_id=$1) events,(select count(*) from business.messages where run_id=$1 and content<>'') hot", id)
	if err != nil {
		t.Fatal(err)
	}
	return row.Messages, row.Events, row.Hot
}
func TestRunHistoryLosslessPublicReadsRestoreAndOriginalKeyReplay(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	originalMessages, originalEvents := historySnapshots(t, s, owner, run)
	before, err := s.RunsModel.FindOne(owner, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	request := historyRequest(run, 0)
	archived, err := ChangeRunHistory(admin, s, request, "archived")
	if err != nil || archived.Status != "completed" || archived.Generation != 1 {
		t.Fatal(archived, err)
	}
	messages, events := historySnapshots(t, s, owner, run)
	if !reflect.DeepEqual(messages, originalMessages) || !reflect.DeepEqual(events, originalEvents) {
		t.Fatal("cold public messages/SSE replay changed")
	}
	mc, ec, hot := historyPayloadCounts(t, s, run.Id)
	if mc != 2 || ec != int64(len(events)) || hot != 0 {
		t.Fatal("payloads were not physically tiered", mc, ec, hot)
	}
	after, err := s.RunsModel.FindOne(owner, run.Id)
	if err != nil || *before != *after {
		t.Fatal("run identity/timestamps changed", err)
	}
	preview, err := GetRunHistory(admin, s, run.Id)
	if err != nil || preview.State != "archived" || !preview.CanRestore {
		t.Fatal(preview, err)
	}
	restored, err := ChangeRunHistory(admin, s, historyRequest(run, 1), "live")
	if err != nil || restored.Generation != 2 {
		t.Fatal(restored, err)
	}
	messages, events = historySnapshots(t, s, owner, run)
	if !reflect.DeepEqual(messages, originalMessages) || !reflect.DeepEqual(events, originalEvents) {
		t.Fatal("restoration changed public payloads")
	}
	mc, ec, hot = historyPayloadCounts(t, s, run.Id)
	if mc != 0 || ec != 0 || hot != 1 {
		t.Fatal("restore did not return payloads", mc, ec, hot)
	}
	replay, err := ChangeRunHistory(admin, s, request, "archived")
	if err != nil || replay != archived {
		t.Fatal("original receipt replay changed newer state", replay, err)
	}
	_, err = RetryRun(owner, s, retryRequest(run))
	operationError(t, err, "RUN_HISTORY_FENCED")
	row, err := s.RunsModel.FindOne(owner, run.Id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RenewLease(context.Background(), s, &types.LeaseReq{RunId: row.Id, DispatchId: row.DispatchId, LeaseGeneration: row.LeaseGeneration})
	operationError(t, err, "LEASE_STALE")
	// Existing terminal/deadline rejection retains its priority. The shared
	// authorization path also enforces the permanent fence for a current actor.
	err = s.Conn.TransactCtx(owner, func(ctx context.Context, tx sqlx.Session) error {
		return authorizeRunTx(ctx, s, tx, row)
	})
	operationError(t, err, "RUN_HISTORY_FENCED")
	if _, err = s.Conn.Exec("update business.messages set content='late write' where run_id=$1", run.Id); err == nil {
		t.Fatal("restored task accepted late payload write")
	}
	if _, err = s.Conn.Exec("update business.run_history_operations set target='live' where id=$1", archived.OperationId); err == nil {
		t.Fatal("immutable receipt changed")
	}
}
func TestRunHistoryLostAgentReplyAndLateAuditRollbackReconcileSameOperation(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	calls := 0
	var operationID string
	historyAgent(t, s, func(w http.ResponseWriter, r *http.Request, receipt client.AgentHistoryReceipt) bool {
		calls++
		if operationID == "" {
			operationID = receipt.OperationId
		}
		if operationID != receipt.OperationId {
			t.Error("recovery created different Agent operation")
		}
		if calls == 1 {
			w.WriteHeader(503)
			return true
		}
		return false
	})
	request := historyRequest(run, 0)
	_, err := ChangeRunHistory(admin, s, request, "archived")
	operationError(t, err, "RUN_HISTORY_UNAVAILABLE")
	mc, ec, hot := historyPayloadCounts(t, s, run.Id)
	if mc != 0 || ec != 0 || hot != 1 {
		t.Fatal("unconfirmed Agent reply moved Go payloads")
	}
	preview, err := GetRunHistory(admin, s, run.Id)
	if err != nil || preview.State != "pending" || preview.Operation.OperationId != operationID {
		t.Fatal(preview, err)
	}
	// Inject failure after payload movement, proving the entire Go completion rolls back.
	if _, err = s.Conn.Exec("CREATE FUNCTION business.synthetic_history_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='run.history.complete' THEN RAISE EXCEPTION 'synthetic completion crash'; END IF; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("CREATE TRIGGER synthetic_history_fail BEFORE INSERT ON business.audit_events FOR EACH ROW EXECUTE FUNCTION business.synthetic_history_fail()"); err != nil {
		t.Fatal(err)
	}
	if _, err = ResumeRunHistory(admin, s, run.Id, operationID); err == nil {
		t.Fatal("late failure accepted")
	}
	mc, ec, hot = historyPayloadCounts(t, s, run.Id)
	if mc != 0 || ec != 0 || hot != 1 {
		t.Fatal("partial Go move after late rollback")
	}
	if _, err = s.Conn.Exec("DROP TRIGGER synthetic_history_fail ON business.audit_events"); err != nil {
		t.Fatal(err)
	}
	recovered, err := ChangeRunHistory(admin, s, request, "archived")
	if err != nil || recovered.OperationId != operationID || recovered.Status != "completed" {
		t.Fatal(recovered, err)
	}
	if calls != 3 {
		t.Fatal("unexpected maintenance effects", calls)
	}
}
func TestRunHistoryPostIOAuthorizationFailureLeavesTraceableRecoverableIntent(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	otherAdmin := operationActor(t, s, owner, "admin", Current(owner).User.TenantId)
	revoke := true
	historyAgent(t, s, func(w http.ResponseWriter, r *http.Request, receipt client.AgentHistoryReceipt) bool {
		if revoke {
			revoke = false
			if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
				t.Error(err)
			}
		}
		return false
	})
	_, err := ChangeRunHistory(admin, s, historyRequest(run, 0), "archived")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked caller got result", err)
	}
	mc, ec, hot := historyPayloadCounts(t, s, run.Id)
	if mc != 0 || ec != 0 || hot != 1 {
		t.Fatal("revoked caller moved Go payloads")
	}
	preview, err := GetRunHistory(otherAdmin, s, run.Id)
	if err != nil || preview.State != "pending" {
		t.Fatal("intent not discoverable", preview, err)
	}
	result, err := ResumeRunHistory(otherAdmin, s, run.Id, preview.Operation.OperationId)
	if err != nil || result.Status != "completed" {
		t.Fatal("current authorized admin could not reconcile", result, err)
	}
	if _, err = GetRunHistory(admin, s, run.Id); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked identity saw history", err)
	}
	_, err = ChangeRunHistory(owner, s, historyRequest(run, 1), "live")
	if !errors.Is(err, errForbidden) {
		t.Fatal("operator maintained history", err)
	}
}
