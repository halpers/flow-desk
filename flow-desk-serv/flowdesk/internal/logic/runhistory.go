package logic

import (
	"context"
	"errors"
	"fmt"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maximumHistoryEvents int64 = 50000
const maximumHistoryCheckpoints int64 = 100000

func historyCurrentTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, id string, manage bool) (*model.Users, *model.Runs, error) {
	actor, err := operationsViewerTx(ctx, s, tx)
	if err != nil {
		return nil, nil, err
	}
	if manage && (actor.Role != "admin" || !permitted(actor, "run:observe")) {
		return nil, nil, errForbidden
	}
	row, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return nil, nil, err
	}
	if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
		return nil, nil, err
	}
	scope := "own"
	if permitted(actor, "run:observe") {
		scope = "tenant"
	}
	row, err = s.RunsModel.FindOneVisibleForShareTx(ctx, tx, model.RunOperationFilter{Tenant: actor.TenantId, Actor: actor.Id, Role: actor.Role, Scope: scope, RunID: id})
	return actor, row, err
}
func historyOperation(row *model.RunHistoryOperations) types.RunHistoryOperationData {
	return types.RunHistoryOperationData{OperationId: row.Id, RunId: row.RunId, Generation: row.Generation, Target: row.Target, Status: row.Status, CheckpointCount: row.CheckpointCount, CheckpointBlobCount: row.CheckpointBlobCount, CheckpointWriteCount: row.CheckpointWriteCount}
}
func historyBusinessProtected(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs) error {
	inventory, err := s.RunsModel.FindLifecycleInventoryTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return err
	}
	// Re-evaluate Go-owned conditions inside the actual intent transaction. A prior
	// observation or UI candidate cannot authorize this write.
	if !inventory.OldEnough || inventory.LeaseActive || inventory.ActiveConversationRunCount > 0 || inventory.UnsettledDispatchCount > 0 || inventory.ActionCount > 0 || inventory.ApprovalCount > 0 || inventory.PendingInputCount > 0 || inventory.ReceiptCount > 0 || inventory.EvidenceCount > 0 || inventory.RetryLinkCount > 0 || inventory.ResourceReferenceCount > 0 || (run.Status != "succeeded" && run.Status != "failed" && run.Status != "expired" && run.Status != "cancelled") {
		return failure(409, "RUN_HISTORY_PROTECTED", "任务受保留期限、业务关联或恢复条件保护")
	}
	if inventory.EventCount > maximumHistoryEvents {
		return failure(409, "RUN_HISTORY_LIMIT", "任务历史超过单次维护上限")
	}
	return nil
}
func GetRunHistory(ctx context.Context, s *svc.ServiceContext, id string) (types.RunHistoryData, error) {
	result := types.RunHistoryData{RunId: id, State: "live", ProtectionReasons: []string{}}
	if !validID(id) {
		return result, errArgument
	}
	lifecycle, observationErr := GetRunLifecycle(ctx, s, id)
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, _, err := historyCurrentTx(ctx, s, tx, id, false)
		if err != nil {
			return err
		}
		result.CanManage = actor.Role == "admin" && permitted(actor, "run:observe")
		row, err := s.RunHistoryOperationsModel.FindLatestTx(ctx, tx, actor.TenantId, id)
		if err == nil {
			result.Generation = row.Generation
			result.Operation = historyOperation(row)
			result.State = row.Target
			if row.Status == "pending" {
				result.State = "pending"
			}
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		if observationErr != nil {
			result.ProtectionReasons = []string{"AGENT_UNAVAILABLE"}
		} else {
			result.ProtectionReasons = lifecycle.ProtectionReasons
			if lifecycle.Business.EventCount > maximumHistoryEvents || lifecycle.Agent.CheckpointCount+lifecycle.Agent.CheckpointBlobCount+lifecycle.Agent.CheckpointWriteCount > maximumHistoryCheckpoints {
				result.ProtectionReasons = append(result.ProtectionReasons, "RUN_HISTORY_LIMIT")
			}
			result.CanArchive = result.CanManage && result.State == "live" && lifecycle.ArchiveCandidate
			result.CanArchive = result.CanArchive && len(result.ProtectionReasons) == 0
			result.CanRestore = result.CanManage && result.State == "archived" && lifecycle.Agent.Available
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryData{}, err
	}
	return result, nil
}
func ChangeRunHistory(ctx context.Context, s *svc.ServiceContext, req *types.RunHistoryChangeReq, target string) (types.RunHistoryOperationData, error) {
	if !validID(req.RunId) || req.ExpectedVersion < 1 || req.ExpectedGeneration < 0 || !req.Confirm {
		return types.RunHistoryOperationData{}, errArgument
	}
	lifecycle, observationErr := GetRunLifecycle(ctx, s, req.RunId)
	result, err := idempotentAuthorized(ctx, s, "run.history."+target, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RunHistoryOperationData, error) {
		return prepareRunHistoryTx(ctx, s, tx, req, target, lifecycle, observationErr)
	}, func(ctx context.Context, tx sqlx.Session, receipt *types.RunHistoryOperationData) error {
		_, _, err := historyCurrentTx(ctx, s, tx, req.RunId, true)
		if receipt.RunId != req.RunId || receipt.Target != target {
			return errForbidden
		}
		return err
	})
	if err != nil {
		return types.RunHistoryOperationData{}, err
	}
	return ResumeRunHistory(ctx, s, req.RunId, result.OperationId)
}

type historyAuthorization func(context.Context, *svc.ServiceContext, sqlx.Session, string, bool) (*model.Users, *model.Runs, error)
type historyValidation func(context.Context, *svc.ServiceContext, sqlx.Session) error

func ResumeRunHistory(ctx context.Context, s *svc.ServiceContext, id, operationID string) (types.RunHistoryOperationData, error) {
	return resumeRunHistory(ctx, s, id, operationID, historyCurrentTx, validateCurrentIdentityTx, "manual")
}
func resumeRunHistory(ctx context.Context, s *svc.ServiceContext, id, operationID string, authorize historyAuthorization, validate historyValidation, source string) (types.RunHistoryOperationData, error) {
	if !validID(id) || !validID(operationID) {
		return types.RunHistoryOperationData{}, errArgument
	}
	var operation *model.RunHistoryOperations
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, _, err := authorize(ctx, s, tx, id, true)
		if err != nil {
			return err
		}
		operation, err = s.RunHistoryOperationsModel.FindOneTx(ctx, tx, actor.TenantId, id, operationID)
		if err != nil {
			return err
		}
		return validate(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryOperationData{}, err
	}
	if operation.Status == "completed" {
		return historyOperation(operation), nil
	}
	receipt, callErr := s.AgentClient.History(ctx, operation.TenantId, historyOperation(operation))
	var result types.RunHistoryOperationData
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, run, err := authorize(ctx, s, tx, id, true)
		if err != nil {
			return err
		}
		current, err := s.RunHistoryOperationsModel.FindOneTx(ctx, tx, actor.TenantId, id, operationID)
		if err != nil {
			return err
		}
		if current.Status == "completed" {
			result = historyOperation(current)
			return validate(ctx, s, tx)
		}
		if callErr != nil {
			return failure(503, "RUN_HISTORY_UNAVAILABLE", "执行历史维护未确认；请从历史窗口继续原操作")
		}
		if current.Target == "archived" {
			if err = historyBusinessProtected(ctx, s, tx, run); err != nil {
				return err
			}
		}
		if err = s.RunHistoryOperationsModel.LockMaintenanceTx(ctx, tx, current.Id); err != nil {
			return err
		}
		if current.Target == "archived" {
			if err = s.MessagePayloadArchivesModel.InsertRunTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.RunEventPayloadArchivesModel.InsertRunTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.MessagesModel.UpdateArchivedPayloadTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.RunEventsModel.UpdateArchivedPayloadTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
		} else {
			if err = s.MessagesModel.UpdateRestoredPayloadTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.RunEventsModel.UpdateRestoredPayloadTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.MessagePayloadArchivesModel.DeleteRestoredTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
			if err = s.RunEventPayloadArchivesModel.DeleteRestoredTx(ctx, tx, current.TenantId, id); err != nil {
				return err
			}
		}
		current.CheckpointCount = receipt.CheckpointCount
		current.CheckpointBlobCount = receipt.CheckpointBlobCount
		current.CheckpointWriteCount = receipt.CheckpointWriteCount
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "run.history.complete", ObjectType: "run", ObjectId: id, Source: source, Summary: fmt.Sprintf("Completed %s history generation %d", current.Target, current.Generation)}); err != nil {
			return err
		}
		if err = validate(ctx, s, tx); err != nil {
			return err
		}
		if err = s.RunHistoryOperationsModel.UpdateCompletedTx(ctx, tx, current); err != nil {
			return err
		}
		current.Status = "completed"
		result = historyOperation(current)
		return nil
	})
	if err != nil {
		return types.RunHistoryOperationData{}, err
	}
	return result, nil
}

func prepareRunHistoryTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, req *types.RunHistoryChangeReq, target string, lifecycle types.RunLifecycleData, observationErr error) (types.RunHistoryOperationData, error) {
	actor, run, err := historyCurrentTx(ctx, s, tx, req.RunId, true)
	if err != nil {
		return types.RunHistoryOperationData{}, err
	}
	return prepareHistoryAuthorizedTx(ctx, s, tx, req, target, lifecycle, observationErr, actor, run, "manual")
}
func prepareHistoryAuthorizedTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, req *types.RunHistoryChangeReq, target string, lifecycle types.RunLifecycleData, observationErr error, actor *model.Users, run *model.Runs, source string) (types.RunHistoryOperationData, error) {
	if observationErr != nil {
		return types.RunHistoryOperationData{}, observationErr
	}
	if !lifecycle.Agent.Available {
		return types.RunHistoryOperationData{}, failure(409, "RUN_HISTORY_PROTECTED", "执行历史尚未建立可信绑定")
	}
	if run.Version != req.ExpectedVersion {
		return types.RunHistoryOperationData{}, failure(409, "VERSION_CONFLICT", "任务状态已变化")
	}
	latest, err := s.RunHistoryOperationsModel.FindLatestTx(ctx, tx, actor.TenantId, run.Id)
	generation := int64(0)
	state := "live"
	if err == nil {
		generation = latest.Generation
		state = latest.Target
		if latest.Status == "pending" {
			return types.RunHistoryOperationData{}, failure(409, "RUN_HISTORY_PENDING", "请继续已有的历史维护操作")
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return types.RunHistoryOperationData{}, err
	}
	if req.ExpectedGeneration != generation || state == target {
		return types.RunHistoryOperationData{}, failure(409, "RUN_HISTORY_CHANGED", "历史存储状态已变化，请刷新")
	}
	if target == "archived" {
		if lifecycle.Agent.CheckpointCount+lifecycle.Agent.CheckpointBlobCount+lifecycle.Agent.CheckpointWriteCount > maximumHistoryCheckpoints {
			return types.RunHistoryOperationData{}, failure(409, "RUN_HISTORY_LIMIT", "任务检查点超过单次维护上限")
		}
		if err = historyBusinessProtected(ctx, s, tx, run); err != nil {
			return types.RunHistoryOperationData{}, err
		}
		if len(lifecycle.Agent.ProtectionReasons) > 0 {
			return types.RunHistoryOperationData{}, failure(409, "RUN_HISTORY_PROTECTED", "执行历史仍受恢复保护")
		}
	}
	operation := &model.RunHistoryOperations{Id: orderedID(), TenantId: actor.TenantId, RunId: run.Id, ActorUserId: actor.Id, Generation: generation + 1, Target: target, Status: "pending"}
	if err = s.RunHistoryOperationsModel.InsertTx(ctx, tx, operation); err != nil {
		return types.RunHistoryOperationData{}, err
	}
	if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "run.history.request", ObjectType: "run", ObjectId: run.Id, Source: source, Summary: fmt.Sprintf("Requested %s history generation %d", target, operation.Generation)}); err != nil {
		return types.RunHistoryOperationData{}, err
	}
	return historyOperation(operation), nil
}
