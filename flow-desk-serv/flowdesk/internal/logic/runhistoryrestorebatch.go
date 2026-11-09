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

func PreviewRunHistoryRestoreBatch(ctx context.Context, s *svc.ServiceContext, req *types.PreviewRunHistoryBatchReq) (types.RunHistoryRestoreBatchPreviewData, error) {
	result := types.RunHistoryRestoreBatchPreviewData{MaximumItems: maximumRunHistoryBatch, CanCreate: true, Items: []types.RunHistoryRestoreBatchPreviewItem{}}
	observations, err := observeHistoryBatch(ctx, s, req.RunIds)
	if err != nil {
		return types.RunHistoryRestoreBatchPreviewData{}, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return err
		}
		for index, runID := range req.RunIds {
			_, run, err := historyCurrentTx(ctx, s, tx, runID, true)
			if err != nil {
				return err
			}
			observed := observations[index]
			if run.Version != observed.Run.Version {
				return failure(409, "LIFECYCLE_CHANGED", "任务状态已变化，请重新预览")
			}
			item := types.RunHistoryRestoreBatchPreviewItem{RunId: runID, ExpectedVersion: run.Version, ProtectionReasons: []string{}}
			latest, latestErr := s.RunHistoryOperationsModel.FindLatestTx(ctx, tx, actor.TenantId, runID)
			if latestErr == nil {
				item.ExpectedGeneration = latest.Generation
				if latest.Status == "pending" {
					item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_PENDING")
				} else if latest.Target != "archived" {
					item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_NOT_ARCHIVED")
				}
			} else if errors.Is(latestErr, model.ErrNotFound) {
				item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_NOT_ARCHIVED")
			} else {
				return latestErr
			}
			if !observed.Agent.Available {
				item.ProtectionReasons = append(item.ProtectionReasons, "AGENT_UNAVAILABLE")
			}
			// Restoration uses the existing single-run conditions. Age, archival
			// candidates and the permanent execution fence do not deny restoration.
			item.CanRestore = len(item.ProtectionReasons) == 0
			result.CanCreate = result.CanCreate && item.CanRestore
			result.Items = append(result.Items, item)
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "run.history.restore_batch_preview", ObjectType: "tenant", ObjectId: actor.TenantId, Source: "manual", Summary: fmt.Sprintf("Previewed %d selected run history restorations", len(req.RunIds))})
	})
	if err != nil {
		return types.RunHistoryRestoreBatchPreviewData{}, err
	}
	return result, nil
}
