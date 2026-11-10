package logic

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"sync/atomic"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type scheduleIntentFault struct{ model.RunHistorySchedulesModel }

func (m scheduleIntentFault) UpdateIntentTx(ctx context.Context, tx sqlx.Session, row *model.RunHistorySchedules, run, operation string) error {
	if err := m.RunHistorySchedulesModel.UpdateIntentTx(ctx, tx, row, run, operation); err != nil {
		return err
	}
	return errors.New("synthetic failure after schedule binding")
}
func TestRunHistoryScheduleConfiguredAgeAndIntentBindingRollback(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	otherTenant := uuid.NewString()
	if _, err := s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: otherTenant, Code: otherTenant, Name: "Synthetic other schedule tenant"}); err != nil {
		t.Fatal(err)
	}
	otherOwner := scheduleOtherAdmin(t, s, owner, otherTenant)
	otherRun := agedHistoryRun(t, s, otherOwner)
	if _, err := s.Conn.Exec("update business.runs set updated_at=clock_timestamp()-interval '120 days' where tenant_id=$1 and id=$2", otherTenant, otherRun.Id); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	historyAgent(t, s, func(_ http.ResponseWriter, _ *http.Request, _ client.AgentHistoryReceipt) bool {
		calls.Add(1)
		return false
	})
	s.Config.RunHistorySchedulerEnabled = true
	req := scheduleRequest(0, true)
	req.MinimumAgeDays = 60
	if _, err := UpdateRunHistorySchedule(admin, s, req); err != nil {
		t.Fatal(err)
	}
	tenant := Current(admin).User.TenantId
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s)
	current, err := GetRunHistorySchedule(admin, s)
	if err != nil || current.LastOutcome != "empty" || calls.Load() != 0 {
		t.Fatal(current, err, calls.Load())
	}
	if _, err = UpdateRunHistorySchedule(admin, s, scheduleRequest(1, true)); err != nil {
		t.Fatal(err)
	}
	original := s.RunHistorySchedulesModel
	s.RunHistorySchedulesModel = scheduleIntentFault{original}
	dueHistorySchedule(t, s, tenant)
	_, err = DispatchHistorySchedule(context.Background(), s)
	if err == nil {
		t.Fatal("expected late binding rollback")
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.run_history_operations where run_id=$1", run.Id); err != nil || count != 0 {
		t.Fatal("orphan operation", count, err)
	}
	if err = s.Conn.QueryRow(&count, "select count(*) from business.run_history_schedules where tenant_id=$1 and last_operation_id is not null", tenant); err != nil || count != 0 {
		t.Fatal("orphan schedule binding", count, err)
	}
	if calls.Load() != 0 {
		t.Fatal("uncommitted intent reached Agent")
	}
	s.RunHistorySchedulesModel = original
	dueHistorySchedule(t, s, tenant)
	dispatchHistorySchedule(t, s)
	current, err = GetRunHistorySchedule(admin, s)
	if err != nil || current.Operation.Generation != 1 || current.LastOutcome != "completed" || calls.Load() != 1 {
		t.Fatal(current, err, calls.Load())
	}
}
