package logic

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func scheduleRequest(revision int64, enabled bool) *types.UpdateRunHistoryScheduleReq {
	return &types.UpdateRunHistoryScheduleReq{ExpectedRevision: revision, Enabled: enabled, MinimumAgeDays: 30, IntervalMinutes: 5, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func enableHistorySchedule(t *testing.T, s *svc.ServiceContext, admin context.Context) types.RunHistoryScheduleData {
	s.Config.RunHistorySchedulerEnabled = true
	t.Helper()
	result, err := UpdateRunHistorySchedule(admin, s, scheduleRequest(0, true))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func dueHistorySchedule(t *testing.T, s *svc.ServiceContext, tenant string) {
	t.Helper()
	_, err := s.Conn.Exec("update business.run_history_schedules set next_run_at=clock_timestamp()-interval '1 second' where tenant_id=$1", tenant)
	if err != nil {
		t.Fatal(err)
	}
}
func dispatchHistorySchedule(t *testing.T, s *svc.ServiceContext) {
	t.Helper()
	found, err := DispatchHistorySchedule(context.Background(), s)
	if err != nil || !found {
		t.Fatal(found, err)
	}
}
func scheduleOtherAdmin(t *testing.T, s *svc.ServiceContext, owner context.Context, tenant string) context.Context {
	t.Helper()
	row := *Current(owner).User
	row.Id = uuid.NewString()
	row.Account = "schedule." + uuid.NewString()
	row.Role = "admin"
	row.TenantId = tenant
	if _, err := s.UsersModel.Insert(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	token, _, err := Login(context.Background(), s, &types.LoginReq{Account: row.Account, Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(context.Background(), s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return WithIdentity(context.Background(), identity)
}

func TestRunHistoryScheduleDefaultsConfirmationRevisionReplayAndScope(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	before, err := GetRunHistorySchedule(admin, s)
	if err != nil || before.Enabled || before.Revision != 0 || before.MinimumAgeDays != 30 || before.IntervalMinutes != 60 {
		t.Fatal(before, err)
	}
	found, err := DispatchHistorySchedule(context.Background(), s)
	if err != nil || found {
		t.Fatal(found, err)
	}
	_, err = GetRunHistorySchedule(owner, s)
	operationError(t, err, "PERMISSION_DENIED")
	request := scheduleRequest(0, true)
	request.Confirm = false
	_, err = UpdateRunHistorySchedule(admin, s, request)
	operationError(t, err, "INVALID_ARGUMENT")
	request.Confirm = true
	request.MinimumAgeDays = 29
	_, err = UpdateRunHistorySchedule(admin, s, request)
	operationError(t, err, "INVALID_ARGUMENT")
	request.MinimumAgeDays = 30
	saved, err := UpdateRunHistorySchedule(admin, s, request)
	if err != nil || !saved.Enabled || saved.Revision != 1 || !saved.AuthorizationValid {
		t.Fatal(saved, err)
	}
	s.Config.RunHistorySchedulerEnabled = false
	dueHistorySchedule(t, s, Current(admin).User.TenantId)
	found, err = DispatchHistorySchedule(context.Background(), s)
	if err != nil || found {
		t.Fatal("runtime gate ignored", found, err)
	}
	s.Config.RunHistorySchedulerEnabled = true
	disabled, err := UpdateRunHistorySchedule(admin, s, scheduleRequest(1, false))
	if err != nil || disabled.Enabled || disabled.Revision != 2 {
		t.Fatal(disabled, err)
	}
	replay, err := UpdateRunHistorySchedule(admin, s, request)
	if err != nil || !reflect.DeepEqual(replay, saved) {
		t.Fatal(replay, err)
	}
	current, err := GetRunHistorySchedule(admin, s)
	if err != nil || current.Enabled || current.Revision != 2 {
		t.Fatal(current, err)
	}
	_, err = UpdateRunHistorySchedule(admin, s, scheduleRequest(1, true))
	operationError(t, err, "RUN_HISTORY_SCHEDULE_CHANGED")
	tenant := uuid.NewString()
	if _, err = s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: tenant, Code: tenant, Name: "Synthetic other tenant"}); err != nil {
		t.Fatal(err)
	}
	otherAdmin := scheduleOtherAdmin(t, s, owner, tenant)
	isolated, err := GetRunHistorySchedule(otherAdmin, s)
	if err != nil || isolated.Revision != 0 {
		t.Fatal(isolated, err)
	}
}
func TestRunHistoryScheduleConcurrentWorkersLosslessReadsAndRestoreExclusion(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	originalMessages, originalEvents := historySnapshots(t, s, owner, run)
	var calls atomic.Int64
	historyAgent(t, s, func(_ http.ResponseWriter, _ *http.Request, _ client.AgentHistoryReceipt) bool {
		calls.Add(1)
		return false
	})
	enableHistorySchedule(t, s, admin)
	tenant := Current(admin).User.TenantId
	dueHistorySchedule(t, s, tenant)
	var group sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		group.Go(func() { _, err := DispatchHistorySchedule(context.Background(), s); results <- err })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate external operation", calls.Load())
	}
	current, err := GetRunHistorySchedule(admin, s)
	if err != nil || current.LastOutcome != "completed" || current.Operation.Status != "completed" || current.Operation.Generation != 1 {
		t.Fatal(current, err)
	}
	messages, events := historySnapshots(t, s, owner, run)
	if !reflect.DeepEqual(originalMessages, messages) || !reflect.DeepEqual(originalEvents, events) {
		t.Fatal("public history changed")
	}
	_, err = ChangeRunHistory(admin, s, historyRequest(run, 1), "live")
	if err != nil {
		t.Fatal(err)
	}
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s) // reset scan cursor
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s) // restored task is still excluded
	latest, err := GetRunHistory(admin, s, run.Id)
	if err != nil || latest.State != "live" || latest.Generation != 2 {
		t.Fatal(latest, err)
	}
	if calls.Load() != 2 {
		t.Fatal("restored history was auto archived again", calls.Load())
	}
}
func TestRunHistoryScheduleRestartResumesOriginalPendingOperation(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	var lost atomic.Bool
	lost.Store(true)
	var operationID string
	historyAgent(t, s, func(w http.ResponseWriter, _ *http.Request, receipt client.AgentHistoryReceipt) bool {
		if operationID != "" && operationID != receipt.OperationId {
			t.Error("operation identity changed")
		}
		operationID = receipt.OperationId
		if lost.Load() {
			w.WriteHeader(503)
			return true
		}
		return false
	})
	enableHistorySchedule(t, s, admin)
	tenant := Current(admin).User.TenantId
	dueHistorySchedule(t, s, tenant)
	found, err := DispatchHistorySchedule(context.Background(), s)
	if !found || err == nil {
		t.Fatal(found, err)
	}
	before, err := GetRunHistorySchedule(admin, s)
	if err != nil || before.Operation.Status != "pending" {
		t.Fatal(before, err)
	}
	_, err = UpdateRunHistorySchedule(admin, s, scheduleRequest(1, true))
	operationError(t, err, "RUN_HISTORY_SCHEDULE_PENDING")
	rebuilt := svc.NewServiceContext(s.Config)
	rebuilt.AgentClient = s.AgentClient
	lost.Store(false)
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, rebuilt)
	after, err := GetRunHistorySchedule(admin, s)
	if err != nil || after.Operation.OperationId != before.Operation.OperationId || after.Operation.Generation != 1 || after.LastOutcome != "completed" {
		t.Fatal(after, err)
	}
	messages, events, hot := historyPayloadCounts(t, s, run.Id)
	if messages != 2 || events < 1 || hot != 0 {
		t.Fatal(messages, events, hot)
	}
}
func TestRunHistoryScheduleDisableAndAuthorizationChangeDuringAgentIO(t *testing.T) {
	for _, mode := range []string{"disable", "auth_version", "role"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			owner, _ := taskContext(t, s)
			admin := knowledgeAdminContext(t, s, owner)
			run := agedHistoryRun(t, s, owner)
			enableHistorySchedule(t, s, admin)
			tenant := Current(admin).User.TenantId
			dueHistorySchedule(t, s, tenant)
			historyAgent(t, s, func(_ http.ResponseWriter, _ *http.Request, _ client.AgentHistoryReceipt) bool {
				switch mode {
				case "disable":
					_, err := UpdateRunHistorySchedule(admin, s, scheduleRequest(1, false))
					if err != nil {
						t.Error(err)
					}
				case "auth_version":
					_, err := s.Conn.Exec("update business.users set auth_version=auth_version+1 where id=$1", Current(admin).User.Id)
					if err != nil {
						t.Error(err)
					}
				case "role":
					_, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(admin).User.Id)
					if err != nil {
						t.Error(err)
					}
				}
				return false
			})
			found, err := DispatchHistorySchedule(context.Background(), s)
			if !found || err == nil {
				t.Fatal(found, err)
			}
			var pending int64
			if err = s.Conn.QueryRow(&pending, "select count(*) from business.run_history_operations where run_id=$1 and status='pending'", run.Id); err != nil || pending != 1 {
				t.Fatal(pending, err)
			}
			messages, events, hot := historyPayloadCounts(t, s, run.Id)
			if messages != 0 || events != 0 || hot != 1 {
				t.Fatal("unauthorized Go completion", messages, events, hot)
			}
			// Another currently authorized admin can reconcile the already persisted
			// operation through the original manual protocol, independent of the policy.
			other := scheduleOtherAdmin(t, s, owner, tenant)
			historyAgent(t, s, nil)
			var operation model.RunHistoryOperations
			if err = s.Conn.QueryRow(&operation, "select * from business.run_history_operations where run_id=$1", run.Id); err != nil {
				t.Fatal(err)
			}
			receipt, err := ResumeRunHistory(other, s, run.Id, operation.Id)
			if err != nil || receipt.Status != "completed" {
				t.Fatal(receipt, err)
			}
		})
	}
}

type scheduleCompletionFault struct{ model.AuditEventsModel }

func (m scheduleCompletionFault) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if row.Operation == "run.history.complete" {
		return errors.New("synthetic final audit failure")
	}
	return m.AuditEventsModel.InsertTx(ctx, tx, row)
}
func TestRunHistoryScheduleLateRollbackAndExpiredClaimFence(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	enableHistorySchedule(t, s, admin)
	tenant := Current(admin).User.TenantId
	dueHistorySchedule(t, s, tenant)
	original := s.AuditEventsModel
	s.AuditEventsModel = scheduleCompletionFault{original}
	_, err := DispatchHistorySchedule(context.Background(), s)
	if err == nil {
		t.Fatal("expected rollback")
	}
	messages, events, hot := historyPayloadCounts(t, s, run.Id)
	if messages != 0 || events != 0 || hot != 1 {
		t.Fatal("partial tier move", messages, events, hot)
	}
	s.AuditEventsModel = original
	dueHistorySchedule(t, s, tenant)
	var claim *model.RunHistorySchedules
	err = s.Conn.TransactCtx(context.Background(), func(ctx context.Context, tx sqlx.Session) error {
		var err error
		claim, err = s.RunHistorySchedulesModel.FindDueForUpdateTx(ctx, tx)
		if err != nil {
			return err
		}
		claim.LeaseToken.String = uuid.NewString()
		claim.LeaseToken.Valid = true
		return s.RunHistorySchedulesModel.UpdateClaimTx(ctx, tx, claim, claim.LeaseToken.String)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.run_history_schedules set lease_until=clock_timestamp()-interval '1 second' where tenant_id=$1", tenant); err != nil {
		t.Fatal(err)
	}
	grant := scheduleGrant{row: claim}
	_, err = resumeRunHistory(context.Background(), s, run.Id, claim.LastOperationId.String, grant.currentTx, grant.finalTx, "scheduled")
	if err == nil {
		t.Fatal("expired worker completed")
	}
	dispatchHistorySchedule(t, s)
	after, err := GetRunHistorySchedule(admin, s)
	if err != nil || after.LastOutcome != "completed" || after.Operation.Generation != 1 {
		t.Fatal(after, err)
	}
}
func TestRunHistoryScheduleRetentionProtectionCursorAndRevokedOwner(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	young := agedHistoryRun(t, s, owner)
	protected := agedHistoryRun(t, s, owner)
	if _, err := s.Conn.Exec("update business.runs set updated_at=clock_timestamp()-interval '10 days' where id=$1", young.Id); err != nil {
		t.Fatal(err)
	}
	// A live task in the same conversation protects the old terminal task.
	newTask(t, owner, s, protected.ConversationId)
	var calls atomic.Int64
	historyAgent(t, s, func(_ http.ResponseWriter, _ *http.Request, _ client.AgentHistoryReceipt) bool {
		calls.Add(1)
		return false
	})
	enableHistorySchedule(t, s, admin)
	tenant := Current(admin).User.TenantId
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s)
	current, err := GetRunHistorySchedule(admin, s)
	if err != nil || current.LastOutcome != "skipped" || calls.Load() != 0 {
		t.Fatal(current, calls.Load(), err)
	}
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s)
	current, err = GetRunHistorySchedule(admin, s)
	if err != nil || current.LastOutcome != "empty" {
		t.Fatal(current, err)
	}
	if _, err = s.Conn.Exec("update business.users set auth_version=auth_version+1 where id=$1", Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	dueHistorySchedule(t, s, tenant)
	_, err = DispatchHistorySchedule(context.Background(), s)
	if err == nil {
		t.Fatal("revoked grant accepted")
	}
	var outcome string
	if err = s.Conn.QueryRow(&outcome, "select last_outcome from business.run_history_schedules where tenant_id=$1", tenant); err != nil || outcome != "authorization_required" {
		t.Fatal(outcome, err)
	}
	if calls.Load() != 0 {
		t.Fatal("revoked grant called Agent")
	}
	// The scheduler is independent of a browser login: it never invents a session.
	if Current(context.Background()) != nil {
		t.Fatal("unexpected ambient identity")
	}
}
