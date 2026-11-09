package logic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/sync/errgroup"
)

const maximumRunHistoryBatch = 10
const maximumHistoryObservations = 5
const runHistoryBatchOperation = "run.history.batch"

func historyBatchAdminTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session) (*model.Users, error) {
	actor, err := operationsViewerTx(ctx, s, tx)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" || !permitted(actor, "run:observe") {
		return nil, errForbidden
	}
	return actor, nil
}
func validateHistoryBatchIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > maximumRunHistoryBatch {
		return errArgument
	}
	previous := ""
	for _, id := range ids {
		if !validID(id) || id != strings.ToLower(id) || id <= previous {
			return errArgument
		}
		previous = id
	}
	return nil
}
func observeHistoryBatch(ctx context.Context, s *svc.ServiceContext, ids []string) ([]types.RunLifecycleData, error) {
	if err := validateHistoryBatchIDs(ids); err != nil {
		return nil, err
	}
	// Authorize every member before starting external observations. No partial
	// preview is exposed if one member or the caller is no longer authorized.
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if _, err := historyBatchAdminTx(ctx, s, tx); err != nil {
			return err
		}
		for _, id := range ids {
			if _, _, err := historyCurrentTx(ctx, s, tx, id, true); err != nil {
				return err
			}
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return nil, err
	}
	observations := make([]types.RunLifecycleData, len(ids))
	group, queryCtx := errgroup.WithContext(ctx)
	group.SetLimit(maximumHistoryObservations)
	for index, id := range ids {
		group.Go(func() error {
			observed, err := GetRunLifecycle(queryCtx, s, id)
			if err == nil {
				observations[index] = observed
			}
			return err
		})
	}
	if err = group.Wait(); err != nil {
		return nil, err
	}
	return observations, nil
}
func PreviewRunHistoryBatch(ctx context.Context, s *svc.ServiceContext, req *types.PreviewRunHistoryBatchReq) (types.RunHistoryBatchPreviewData, error) {
	result := types.RunHistoryBatchPreviewData{MaximumItems: maximumRunHistoryBatch, CanCreate: true, Items: []types.RunHistoryBatchPreviewItem{}}
	observations, err := observeHistoryBatch(ctx, s, req.RunIds)
	if err != nil {
		return types.RunHistoryBatchPreviewData{}, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return err
		}
		for index, id := range req.RunIds {
			_, run, err := historyCurrentTx(ctx, s, tx, id, true)
			if err != nil {
				return err
			}
			observed := observations[index]
			if run.Version != observed.Run.Version {
				return failure(409, "LIFECYCLE_CHANGED", "任务状态已变化，请重新预览")
			}
			item := types.RunHistoryBatchPreviewItem{RunId: id, ExpectedVersion: run.Version, ProtectionReasons: append([]string{}, observed.ProtectionReasons...), CanArchive: observed.ArchiveCandidate}
			latest, latestErr := s.RunHistoryOperationsModel.FindLatestTx(ctx, tx, actor.TenantId, id)
			if latestErr == nil {
				item.ExpectedGeneration = latest.Generation
				if latest.Status == "pending" {
					item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_PENDING")
					item.CanArchive = false
				} else if latest.Target == "archived" {
					item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_ARCHIVED")
					item.CanArchive = false
				}
			} else if !errors.Is(latestErr, model.ErrNotFound) {
				return latestErr
			}
			if observed.Business.EventCount > maximumHistoryEvents || observed.Agent.CheckpointCount+observed.Agent.CheckpointBlobCount+observed.Agent.CheckpointWriteCount > maximumHistoryCheckpoints {
				item.ProtectionReasons = append(item.ProtectionReasons, "RUN_HISTORY_LIMIT")
				item.CanArchive = false
			}
			if err = historyBusinessProtected(ctx, s, tx, run); err != nil {
				var businessErr *BusinessError
				if !errors.As(err, &businessErr) {
					return err
				}
				if !slices.Contains(item.ProtectionReasons, businessErr.Code) {
					item.ProtectionReasons = append(item.ProtectionReasons, businessErr.Code)
				}
				item.CanArchive = false
			}
			result.CanCreate = result.CanCreate && item.CanArchive
			result.Items = append(result.Items, item)
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "run.history.batch_preview", ObjectType: "tenant", ObjectId: actor.TenantId, Source: "manual", Summary: fmt.Sprintf("Previewed %d selected run histories", len(req.RunIds))})
	})
	if err != nil {
		return types.RunHistoryBatchPreviewData{}, err
	}
	return result, nil
}
func CreateRunHistoryBatch(ctx context.Context, s *svc.ServiceContext, req *types.CreateRunHistoryBatchReq) (types.RunHistoryBatchReceipt, error) {
	ids := make([]string, len(req.Items))
	for index, item := range req.Items {
		ids[index] = item.RunId
		if item.ExpectedVersion < 1 || item.ExpectedGeneration < 0 {
			return types.RunHistoryBatchReceipt{}, errArgument
		}
	}
	if !req.Confirm {
		return types.RunHistoryBatchReceipt{}, errArgument
	}
	if err := validateHistoryBatchIDs(ids); err != nil {
		return types.RunHistoryBatchReceipt{}, err
	}
	if !idempotencyKeyPattern.MatchString(req.IdempotencyKey) {
		return types.RunHistoryBatchReceipt{}, errArgument
	}
	// A committed key can be recovered without contacting Agent. The final
	// idempotency transaction still checks the original body and all members.
	needsObservation := true
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return err
		}
		_, err = s.ApiIdempotencyModel.FindOneByScopeTx(ctx, tx, actor.TenantId, actor.Id, runHistoryBatchOperation, HashToken(req.IdempotencyKey))
		if err == nil {
			needsObservation = false
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryBatchReceipt{}, err
	}
	var observations []types.RunLifecycleData
	var observationErr error
	if needsObservation {
		observations, observationErr = observeHistoryBatch(ctx, s, ids)
	}
	return idempotentAuthorized(ctx, s, runHistoryBatchOperation, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RunHistoryBatchReceipt, error) {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return types.RunHistoryBatchReceipt{}, err
		}
		if observationErr != nil {
			return types.RunHistoryBatchReceipt{}, observationErr
		}
		if len(observations) != len(ids) {
			return types.RunHistoryBatchReceipt{}, failure(409, "RUN_HISTORY_BATCH_CHANGED", "批次请求登记已变化，请读取最近批次并重新核对")
		}
		batch := &model.RunHistoryBatches{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, ItemCount: int64(len(ids))}
		if err = s.RunHistoryBatchesModel.InsertTx(ctx, tx, batch); err != nil {
			return types.RunHistoryBatchReceipt{}, err
		}
		for index, item := range req.Items {
			operation, err := prepareRunHistoryTx(ctx, s, tx, &types.RunHistoryChangeReq{RunId: item.RunId, ExpectedVersion: item.ExpectedVersion, ExpectedGeneration: item.ExpectedGeneration, Confirm: true}, "archived", observations[index], nil)
			if err != nil {
				return types.RunHistoryBatchReceipt{}, err
			}
			if err = s.RunHistoryBatchItemsModel.InsertTx(ctx, tx, &model.RunHistoryBatchItems{Id: orderedID(), TenantId: actor.TenantId, BatchId: batch.Id, OperationId: operation.OperationId, Ordinal: int64(index + 1)}); err != nil {
				return types.RunHistoryBatchReceipt{}, err
			}
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: runHistoryBatchOperation, ObjectType: "run_history_batch", ObjectId: batch.Id, Source: "manual", Summary: fmt.Sprintf("Registered %d confirmed run history intents", len(ids))}); err != nil {
			return types.RunHistoryBatchReceipt{}, err
		}
		return types.RunHistoryBatchReceipt{BatchId: batch.Id}, nil
	}, func(ctx context.Context, tx sqlx.Session, receipt *types.RunHistoryBatchReceipt) error {
		data, err := historyBatchDataTx(ctx, s, tx, receipt.BatchId)
		if err != nil {
			return err
		}
		if len(data.Items) != len(ids) {
			return errForbidden
		}
		for index, item := range data.Items {
			if item.RunId != ids[index] || item.Target != "archived" || item.Generation != req.Items[index].ExpectedGeneration+1 {
				return errForbidden
			}
		}
		return nil
	})
}
func batchData(row *model.RunHistoryBatches, operations []model.RunHistoryOperations) types.RunHistoryBatchData {
	result := types.RunHistoryBatchData{BatchId: row.Id, ActorUserId: row.ActorUserId, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), Status: "completed", Items: []types.RunHistoryOperationData{}}
	for index := range operations {
		result.Items = append(result.Items, historyOperation(&operations[index]))
		if operations[index].Status == "completed" {
			result.CompletedCount++
		} else {
			result.Status = "pending"
		}
	}
	return result
}
func historyBatchDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, id string) (types.RunHistoryBatchData, error) {
	actor, err := historyBatchAdminTx(ctx, s, tx)
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	row, err := s.RunHistoryBatchesModel.FindOneTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	operations, err := s.RunHistoryBatchItemsModel.ListOperationsTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	if int64(len(operations)) != row.ItemCount {
		return types.RunHistoryBatchData{}, failure(503, "RUN_HISTORY_BATCH_INCOMPLETE", "批次登记不完整，请联系运维核对")
	}
	for _, operation := range operations {
		if _, _, err = historyCurrentTx(ctx, s, tx, operation.RunId, true); err != nil {
			return types.RunHistoryBatchData{}, err
		}
	}
	return batchData(row, operations), nil
}
func GetRunHistoryBatch(ctx context.Context, s *svc.ServiceContext, id string) (types.RunHistoryBatchData, error) {
	var result types.RunHistoryBatchData
	if !validID(id) {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		result, err = historyBatchDataTx(ctx, s, tx, id)
		if err != nil {
			return err
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	return result, nil
}
func ResumeRunHistoryBatch(ctx context.Context, s *svc.ServiceContext, req *types.ResumeRunHistoryBatchReq) (types.RunHistoryBatchData, error) {
	if !validID(req.BatchId) || !validID(req.OperationId) {
		return types.RunHistoryBatchData{}, errArgument
	}
	before, err := GetRunHistoryBatch(ctx, s, req.BatchId)
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	chosen := -1
	for index, item := range before.Items {
		if item.OperationId == req.OperationId {
			chosen = index
			break
		}
	}
	if chosen < 0 {
		return types.RunHistoryBatchData{}, errMissing
	}
	_, callErr := ResumeRunHistory(ctx, s, before.Items[chosen].RunId, req.OperationId)
	// Never return stale batch members after slow IO or an authorization error.
	after, err := GetRunHistoryBatch(ctx, s, req.BatchId)
	if err != nil {
		return types.RunHistoryBatchData{}, err
	}
	if callErr != nil {
		return types.RunHistoryBatchData{}, callErr
	}
	return after, nil
}
func ListRunHistoryBatches(ctx context.Context, s *svc.ServiceContext, req *types.ListRunHistoryBatchesReq) (types.RunHistoryBatchPage, error) {
	result := types.RunHistoryBatchPage{Items: []types.RunHistoryBatchData{}}
	if req.Limit == 0 {
		req.Limit = 10
	}
	if req.Limit < 1 || req.Limit > maximumRunHistoryBatch {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := historyBatchAdminTx(ctx, s, tx)
		if err != nil {
			return err
		}
		scope := "run-history-batches-v1:" + actor.TenantId + ":" + actor.Id
		position, err := decodeCursor(req.Cursor, scope)
		if err != nil {
			return err
		}
		before := time.Now().Add(time.Hour)
		beforeID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
		if position != "" {
			parts := strings.Split(position, "|")
			if len(parts) != 2 || !validID(parts[1]) {
				return errArgument
			}
			before, err = time.Parse(time.RFC3339Nano, parts[0])
			if err != nil {
				return errArgument
			}
			beforeID = parts[1]
		}
		rows, err := s.RunHistoryBatchesModel.ListPageTx(ctx, tx, actor.TenantId, before, beforeID, req.Limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > req.Limit
		if result.HasMore {
			rows = rows[:req.Limit]
		}
		// Acquire task locks in a global stable order across the whole page.
		members := make(map[string][]model.RunHistoryOperations, len(rows))
		visibility := map[string]bool{}
		ids := []string{}
		for _, row := range rows {
			items, err := s.RunHistoryBatchItemsModel.ListOperationsTx(ctx, tx, actor.TenantId, row.Id)
			if err != nil {
				return err
			}
			if int64(len(items)) != row.ItemCount {
				return failure(503, "RUN_HISTORY_BATCH_INCOMPLETE", "批次登记不完整")
			}
			members[row.Id] = items
			for _, item := range items {
				if _, found := visibility[item.RunId]; !found {
					visibility[item.RunId] = false
					ids = append(ids, item.RunId)
				}
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			_, _, err = historyCurrentTx(ctx, s, tx, id, true)
			if errors.Is(err, errForbidden) || errors.Is(err, errMissing) || errors.Is(err, model.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			visibility[id] = true
		}
		for index := range rows {
			visible := true
			for _, item := range members[rows[index].Id] {
				visible = visible && visibility[item.RunId]
			}
			if visible {
				result.Items = append(result.Items, batchData(&rows[index], members[rows[index].Id]))
			}
		}
		if result.HasMore {
			last := rows[len(rows)-1]
			result.NextCursor = encodeCursor(scope, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"|"+last.Id)
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunHistoryBatchPage{}, err
	}
	return result, nil
}
