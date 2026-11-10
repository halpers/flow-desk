package logic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const minimumScheduleAgeDays int64 = 30
const maximumScheduleAgeDays int64 = 3650
const minimumScheduleIntervalMinutes int64 = 5
const maximumScheduleIntervalMinutes int64 = 10080
const defaultScheduleIntervalMinutes int64 = 60
const historyScheduleTimeout = 30 * time.Second

func scheduleOwnerValid(row *model.RunHistorySchedules, actor *model.Users) bool {
	return actor.Enabled && actor.TenantId == row.TenantId && actor.Id == row.OwnerUserId && actor.AuthVersion == row.OwnerAuthVersion && actor.Role == "admin" && permitted(actor, "run:observe") && permitted(actor, "conversation:use")
}
func scheduleDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.RunHistorySchedules) (types.RunHistoryScheduleData, error) {
	result := types.RunHistoryScheduleData{RuntimeEnabled: s.Config.RunHistorySchedulerEnabled, Enabled: row.Enabled, Revision: row.Revision, MinimumAgeDays: row.MinimumAgeDays, IntervalMinutes: row.IntervalMinutes, OwnerUserId: row.OwnerUserId, NextRunAt: row.NextRunAt.UTC().Format(time.RFC3339Nano), LastOutcome: row.LastOutcome, LastRunId: row.LastRunId.String}
	if row.LastCheckedAt.Valid {
		result.LastCheckedAt = row.LastCheckedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	owner, err := s.UsersModel.FindOneTx(ctx, tx, row.OwnerUserId)
	if err != nil {
		return result, err
	}
	result.AuthorizationValid = scheduleOwnerValid(row, owner)
	if row.LastOperationId.Valid {
		operation, err := s.RunHistoryOperationsModel.FindOneTx(ctx, tx, row.TenantId, row.LastRunId.String, row.LastOperationId.String)
		if err != nil {
			return result, err
		}
		result.Operation = historyOperation(operation)
	}
	return result, nil
}
func GetRunHistorySchedule(ctx context.Context, s *svc.ServiceContext) (types.RunHistoryScheduleData, error) {
	result := types.RunHistoryScheduleData{RuntimeEnabled: s.Config.RunHistorySchedulerEnabled, MinimumAgeDays: minimumScheduleAgeDays, IntervalMinutes: defaultScheduleIntervalMinutes, LastOutcome: "never"}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return err
		}
		row, err := s.RunHistorySchedulesModel.FindOneForShareTx(ctx, tx, actor.TenantId)
		if errors.Is(err, model.ErrNotFound) {
			return validateCurrentIdentityTx(ctx, s, tx)
		}
		if err != nil {
			return err
		}
		result, err = scheduleDataTx(ctx, s, tx, row)
		if err != nil {
			return err
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryScheduleData{}, err
	}
	return result, nil
}
func UpdateRunHistorySchedule(ctx context.Context, s *svc.ServiceContext, req *types.UpdateRunHistoryScheduleReq) (types.RunHistoryScheduleData, error) {
	if !req.Confirm || req.ExpectedRevision < 0 || req.MinimumAgeDays < minimumScheduleAgeDays || req.MinimumAgeDays > maximumScheduleAgeDays || req.IntervalMinutes < minimumScheduleIntervalMinutes || req.IntervalMinutes > maximumScheduleIntervalMinutes {
		return types.RunHistoryScheduleData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "run.history.schedule", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RunHistoryScheduleData, error) {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		if err = s.RunHistorySchedulesModel.InsertIfAbsentTx(ctx, tx, actor.TenantId, actor.Id, actor.AuthVersion); err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		row, err := s.RunHistorySchedulesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		if row.Revision != req.ExpectedRevision {
			return types.RunHistoryScheduleData{}, failure(409, "RUN_HISTORY_SCHEDULE_CHANGED", "定时归档策略已变化，请刷新后重新确认")
		}
		if row.LastOperationId.Valid {
			operation, err := s.RunHistoryOperationsModel.FindOneTx(ctx, tx, row.TenantId, row.LastRunId.String, row.LastOperationId.String)
			if err != nil {
				return types.RunHistoryScheduleData{}, err
			}
			if operation.Status == "pending" && (req.Enabled || req.MinimumAgeDays != row.MinimumAgeDays || req.IntervalMinutes != row.IntervalMinutes) {
				return types.RunHistoryScheduleData{}, failure(409, "RUN_HISTORY_SCHEDULE_PENDING", "请先从原任务历史窗口核对待确认维护操作")
			}
		}
		row.Enabled = req.Enabled
		row.MinimumAgeDays = req.MinimumAgeDays
		row.IntervalMinutes = req.IntervalMinutes
		row.OwnerUserId = actor.Id
		row.OwnerAuthVersion = actor.AuthVersion
		if err = s.RunHistorySchedulesModel.UpdatePolicyTx(ctx, tx, row); err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "run.history.schedule", ObjectType: "run_history_schedule", ObjectId: row.Id, Source: "manual", Summary: fmt.Sprintf("Confirmed schedule revision %d enabled=%t minimum_age_days=%d interval_minutes=%d", row.Revision+1, row.Enabled, row.MinimumAgeDays, row.IntervalMinutes)}); err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		row, err = s.RunHistorySchedulesModel.FindOneForShareTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.RunHistoryScheduleData{}, err
		}
		return scheduleDataTx(ctx, s, tx, row)
	}, func(ctx context.Context, tx sqlx.Session, _ *types.RunHistoryScheduleData) error {
		_, err := historyBatchAdminTx(ctx, s, tx)
		return err
	})
}

// scheduleGrant is never derived from HTTP/model input. It is a fenced claim of
// an explicitly persisted authorization, used only by history maintenance.
type scheduleGrant struct{ row *model.RunHistorySchedules }

func (grant scheduleGrant) actorTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, final bool) (*model.Users, error) {
	if _, err := s.RunHistorySchedulesModel.FindClaimForShareTx(ctx, tx, grant.row); err != nil {
		return nil, err
	}
	var actor *model.Users
	var err error
	if final {
		actor, err = s.UsersModel.FindOneForShareTx(ctx, tx, grant.row.TenantId, grant.row.OwnerUserId)
	} else {
		actor, err = s.UsersModel.FindOneTx(ctx, tx, grant.row.OwnerUserId)
	}
	if err != nil {
		return nil, err
	}
	if !scheduleOwnerValid(grant.row, actor) {
		return nil, errForbidden
	}
	return actor, nil
}
func (grant scheduleGrant) currentTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, id string, _ bool) (*model.Users, *model.Runs, error) {
	actor, err := grant.actorTx(ctx, s, tx, false)
	if err != nil {
		return nil, nil, err
	}
	if _, err = s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, id); err != nil {
		return nil, nil, err
	}
	if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
		return nil, nil, err
	}
	run, err := s.RunsModel.FindOneVisibleForShareTx(ctx, tx, model.RunOperationFilter{Tenant: actor.TenantId, Actor: actor.Id, Role: actor.Role, Scope: "tenant", RunID: id})
	return actor, run, err
}
func (grant scheduleGrant) finalTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session) error {
	_, err := grant.actorTx(ctx, s, tx, true)
	return err
}

// DispatchHistorySchedule is bounded to one policy and one candidate/operation.
// Separate workers coordinate through DB claims; no transaction spans Agent IO.
func DispatchHistorySchedule(ctx context.Context, s *svc.ServiceContext) (bool, error) {
	if !s.Config.RunHistorySchedulerEnabled {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, historyScheduleTimeout)
	defer cancel()
	var row *model.RunHistorySchedules
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		row, err = s.RunHistorySchedulesModel.FindDueForUpdateTx(ctx, tx)
		if err != nil {
			return err
		}
		token := orderedID()
		if err = s.RunHistorySchedulesModel.UpdateClaimTx(ctx, tx, row, token); err != nil {
			return err
		}
		row.LeaseToken = sql.NullString{String: token, Valid: true}
		return nil
	})
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ctx, span := otel.Tracer("flowdesk.scheduler").Start(ctx, "run.history.schedule")
	defer span.End()
	grant := scheduleGrant{row: row}
	outcome, processErr := processHistorySchedule(ctx, s, grant)
	if processErr != nil {
		outcome = "unavailable"
		if errors.Is(processErr, errForbidden) || errors.Is(processErr, ErrUnauthorized) {
			outcome = "authorization_required"
		}
	}
	// A cancelled owner update or expired lease cannot overwrite newer progress.
	finishErr := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		return s.RunHistorySchedulesModel.UpdateFinishedTx(ctx, tx, row, outcome)
	})
	if finishErr != nil {
		return true, finishErr
	}
	return true, processErr
}
func processHistorySchedule(ctx context.Context, s *svc.ServiceContext, grant scheduleGrant) (string, error) {
	row := grant.row
	var candidate *model.Runs
	var pending *model.RunHistoryOperations
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if _, err := grant.actorTx(ctx, s, tx, false); err != nil {
			return err
		}
		if row.LastOperationId.Valid {
			operation, err := s.RunHistoryOperationsModel.FindOneTx(ctx, tx, row.TenantId, row.LastRunId.String, row.LastOperationId.String)
			if err != nil {
				return err
			}
			if operation.Status == "pending" || row.LastOutcome == "pending" {
				pending = operation
				return grant.finalTx(ctx, s, tx)
			}
		}
		var err error
		candidate, err = s.RunsModel.FindHistoryScheduleCandidateTx(ctx, tx, row.TenantId, row.CursorRunId.String, row.MinimumAgeDays)
		if errors.Is(err, model.ErrNotFound) {
			candidate = nil
			return grant.finalTx(ctx, s, tx)
		}
		if err != nil {
			return err
		}
		if _, _, err = grant.currentTx(ctx, s, tx, candidate.Id, true); err != nil {
			return err
		}
		return grant.finalTx(ctx, s, tx)
	})
	if err != nil {
		return "", err
	}
	if pending != nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("flowdesk.run_id", pending.RunId))
		_, err = resumeRunHistory(ctx, s, pending.RunId, pending.Id, grant.currentTx, grant.finalTx, "scheduled")
		if err != nil {
			return "pending", err
		}
		return "completed", nil
	}
	if candidate == nil {
		row.CursorRunId = sql.NullString{}
		return "empty", nil
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("flowdesk.run_id", candidate.Id))
	// Cursor advancement is persisted only after a completed observation/decision;
	// transient failures keep the candidate available for the next attempt.
	observed, err := s.AgentClient.Lifecycle(ctx, row.TenantId, candidate.Id)
	if err != nil {
		return "", err
	}
	var operation types.RunHistoryOperationData
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, run, err := grant.currentTx(ctx, s, tx, candidate.Id, true)
		if err != nil {
			return err
		}
		oldEnough, err := s.RunsModel.ExistsHistoryScheduleAgeTx(ctx, tx, row.TenantId, run.Id, row.MinimumAgeDays)
		if err != nil {
			return err
		}
		if !oldEnough {
			return failure(409, "RUN_HISTORY_PROTECTED", "任务未满足当前策略保留期限")
		}
		operation, err = prepareHistoryAuthorizedTx(ctx, s, tx, &types.RunHistoryChangeReq{RunId: run.Id, ExpectedVersion: candidate.Version, ExpectedGeneration: 0, Confirm: true}, "archived", types.RunLifecycleData{Agent: observed}, nil, actor, run, "scheduled")
		if err != nil {
			return err
		}
		if err = s.RunHistorySchedulesModel.UpdateIntentTx(ctx, tx, row, run.Id, operation.OperationId); err != nil {
			return err
		}
		return grant.finalTx(ctx, s, tx)
	})
	if err != nil {
		var problem *BusinessError
		if errors.As(err, &problem) && (problem.Status == 409) {
			row.CursorRunId = sql.NullString{String: candidate.Id, Valid: true}
			return "skipped", nil
		}
		return "", err
	}
	row.CursorRunId = sql.NullString{String: candidate.Id, Valid: true}
	row.LastRunId = row.CursorRunId
	row.LastOperationId = sql.NullString{String: operation.OperationId, Valid: true}
	_, err = resumeRunHistory(ctx, s, candidate.Id, operation.OperationId, grant.currentTx, grant.finalTx, "scheduled")
	if err != nil {
		return "pending", err
	}
	return "completed", nil
}
