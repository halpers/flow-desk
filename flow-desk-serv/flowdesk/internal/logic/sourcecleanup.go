package logic

import (
	"context"
	"encoding/json"
	"errors"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maxRegisteredSourceCopies int64 = 100
const sourceObjectRegistered = "registered"
const sourceObjectCleanupPending = "cleanup_pending"
const sourceObjectRetired = "retired"

func registerSourceObjectTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.DocumentVersions, store, key string) error {
	count, err := s.SourceObjectsModel.CountRegisteredTx(ctx, tx, row.TenantId, row.Id)
	if err != nil {
		return err
	}
	existing, err := s.SourceObjectsModel.FindOneByStorageTx(ctx, tx, store, key)
	if err == nil {
		if existing.TenantId != row.TenantId || existing.VersionId != row.Id || existing.SourceEpoch != row.SourceEpoch || existing.ContentHash != row.ContentHash || existing.SourceSize != row.SourceSize || existing.Status != sourceObjectRegistered {
			return sourceIntegrity()
		}
		return nil
	}
	if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if count >= maxRegisteredSourceCopies {
		return failure(429, "SOURCE_COPY_LIMIT", "归档副本已达到上限，请先恢复并清理旧副本")
	}
	id := uuid.NewSHA1(uuid.MustParse(row.Id), []byte(store+"\n"+key)).String()
	return s.SourceObjectsModel.InsertIfAbsentTx(ctx, tx, &model.SourceObjects{Id: id, TenantId: row.TenantId, VersionId: row.Id, SourceEpoch: row.SourceEpoch, StoreId: store, ObjectKey: key, ContentHash: row.ContentHash, SourceSize: row.SourceSize})
}
func sourceCleanupError(err error) error {
	if errors.Is(err, client.ErrSourceCleanupUnsupported) {
		return failure(409, "SOURCE_CLEANUP_UNSUPPORTED", "存储不支持安全清理，请检查桶版本控制、权限及兼容能力")
	}
	if errors.Is(err, client.ErrSourceIntegrity) {
		return sourceIntegrity()
	}
	return sourceUnavailable()
}
func archiveCopyData(s *svc.ServiceContext, row *model.DocumentVersions, object *model.SourceObjects, originalValid bool) types.ArchiveCopyItem {
	reasons := []string{}
	if !s.Config.FileStoreCleanupEnabled {
		reasons = append(reasons, "cleanup_disabled")
	}
	if object.Status != sourceObjectRegistered {
		reasons = append(reasons, object.Status)
	}
	if row.SourceStorage != "postgres" {
		reasons = append(reasons, "restore_required")
	}
	if row.CleanupState != versionRetained {
		reasons = append(reasons, "version_retired")
	}
	if row.SourceEpoch <= object.SourceEpoch {
		reasons = append(reasons, "current_generation")
	}
	if s.SourceStore == nil || s.SourceStore.ID() != object.StoreId {
		reasons = append(reasons, "store_mismatch")
	}
	if !originalValid {
		reasons = append(reasons, "original_unverified")
	}
	return types.ArchiveCopyItem{Id: object.Id, SourceEpoch: object.SourceEpoch, SourceSize: object.SourceSize, Status: object.Status, Eligible: len(reasons) == 0, Reasons: reasons}
}
func validDatabaseOriginal(row *model.DocumentVersions) bool {
	return row.SourceStorage == "postgres" && row.SourceFormat == "pdf" && row.CleanupState == versionRetained && int64(len(row.SourceBytes)) == row.SourceSize && HashToken(string(row.SourceBytes)) == row.ContentHash
}
func ListArchiveCopies(ctx context.Context, s *svc.ServiceContext, req *types.VersionPagesReq) (types.ArchiveCopiesData, error) {
	result := types.ArchiveCopiesData{VersionId: req.VersionId, Enabled: s.Config.FileStoreCleanupEnabled, Items: []types.ArchiveCopyItem{}}
	if !validID(req.DocumentId) || !validID(req.VersionId) {
		return result, errArgument
	}
	eligible := false
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		result.SourceEpoch = row.SourceEpoch
		objects, err := s.SourceObjectsModel.ListByVersionTx(ctx, tx, row.TenantId, row.Id)
		if err != nil {
			return err
		}
		verified := validDatabaseOriginal(row)
		for i := range objects {
			item := archiveCopyData(s, row, &objects[i], verified)
			eligible = eligible || item.Eligible
			result.Items = append(result.Items, item)
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err == nil && eligible {
		cleaner, ok := s.SourceStore.(client.SourceRetirer)
		if !ok {
			return result, failure(409, "SOURCE_CLEANUP_UNSUPPORTED", "当前存储不支持副本清理")
		}
		if checkErr := cleaner.CheckRetirement(ctx); checkErr != nil {
			return result, sourceCleanupError(checkErr)
		}
	}
	return result, err
}
func CleanupArchiveCopy(ctx context.Context, s *svc.ServiceContext, req *types.CleanupArchiveCopyReq) (types.ArchiveCopyItem, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.ArchiveCopyItem{}, err
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || !validID(req.ObjectId) || !req.Confirm || req.ExpectedSourceEpoch < 1 {
		return types.ArchiveCopyItem{}, errArgument
	}
	if !s.Config.FileStoreCleanupEnabled {
		return types.ArchiveCopyItem{}, failure(409, "SOURCE_CLEANUP_DISABLED", "归档副本清理尚未启用")
	}
	if _, err = sourceAdminVersion(ctx, s, req.DocumentId, req.VersionId); err != nil {
		return types.ArchiveCopyItem{}, err
	}
	cleaner, ok := s.SourceStore.(client.SourceRetirer)
	if !ok {
		return types.ArchiveCopyItem{}, failure(409, "SOURCE_CLEANUP_UNSUPPORTED", "当前存储不支持副本清理")
	}
	if err = cleaner.CheckRetirement(ctx); err != nil {
		return types.ArchiveCopyItem{}, sourceCleanupError(err)
	}
	return idempotentAuthorized(ctx, s, "knowledge.source_cleanup", req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.ArchiveCopyItem, error) {
		if _, err := sourceAdminDocumentTx(ctx, s, tx, req.DocumentId); err != nil {
			return types.ArchiveCopyItem{}, err
		}
		row, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.VersionId)
		if err != nil {
			return types.ArchiveCopyItem{}, err
		}
		if row.DocumentId != req.DocumentId {
			return types.ArchiveCopyItem{}, errMissing
		}
		object, err := s.SourceObjectsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.ObjectId)
		if err != nil {
			return types.ArchiveCopyItem{}, err
		}
		if object.VersionId != row.Id {
			return types.ArchiveCopyItem{}, errMissing
		}
		if row.SourceEpoch != req.ExpectedSourceEpoch {
			return types.ArchiveCopyItem{}, failure(409, "SOURCE_CHANGED", "原件状态已变化，请重新预览")
		}
		item := archiveCopyData(s, row, object, validDatabaseOriginal(row))
		if !item.Eligible {
			return types.ArchiveCopyItem{}, failure(409, "SOURCE_COPY_PROTECTED", "归档副本仍受保护，请核对数据库原件与存储配置")
		}
		if _, ok := s.SourceStore.(client.SourceRetirer); !ok {
			return types.ArchiveCopyItem{}, failure(409, "SOURCE_CLEANUP_UNSUPPORTED", "当前存储不支持副本清理")
		}
		if err = s.SourceObjectsModel.UpdateStatusTx(ctx, tx, actor.TenantId, object.Id, sourceObjectRegistered, sourceObjectCleanupPending); err != nil {
			return types.ArchiveCopyItem{}, err
		}
		if err = s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: orderedID(), TenantId: actor.TenantId, AggregateId: row.DocumentId, DispatchId: uuid.NewSHA1(uuid.MustParse(object.Id), []byte("knowledge.source_cleanup")).String(), Topic: "knowledge.source_cleanup", Payload: marshal(map[string]string{"object_id": object.Id}), Status: "pending"}); err != nil {
			return types.ArchiveCopyItem{}, err
		}
		err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_cleanup", ObjectType: "source_object", ObjectId: object.Id, Source: "manual", Summary: "Redundant archived bytes cleanup confirmed; permanent object marker retained"})
		object.Status = sourceObjectCleanupPending
		return archiveCopyData(s, row, object, true), err
	}, func(ctx context.Context, tx sqlx.Session, result *types.ArchiveCopyItem) error {
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		object, err := s.SourceObjectsModel.FindOneForUpdateTx(ctx, tx, row.TenantId, result.Id)
		if err != nil {
			return err
		}
		if object.VersionId != row.Id {
			return errMissing
		}
		*result = archiveCopyData(s, row, object, validDatabaseOriginal(row))
		return nil
	})
}
func CleanupSourceObject(ctx context.Context, s *svc.ServiceContext, event *model.OutboxEvents) error {
	var payload struct {
		ObjectID string `json:"object_id"`
	}
	if json.Unmarshal([]byte(event.Payload), &payload) != nil || !validID(payload.ObjectID) {
		return errArgument
	}
	object, err := s.SourceObjectsModel.FindOneByTenantIdId(ctx, event.TenantId, payload.ObjectID)
	if err != nil {
		return err
	}
	row, err := s.DocumentVersionsModel.FindOneByTenantIdId(ctx, event.TenantId, object.VersionId)
	if err != nil {
		return err
	}
	if row.DocumentId != event.AggregateId || object.ContentHash != row.ContentHash || object.SourceSize != row.SourceSize {
		return errForbidden
	}
	if object.Status == sourceObjectRetired {
		return nil
	}
	if !s.Config.FileStoreCleanupEnabled || object.Status != sourceObjectCleanupPending || s.SourceStore == nil || s.SourceStore.ID() != object.StoreId || row.SourceEpoch <= object.SourceEpoch || row.SourceKey == object.ObjectKey {
		return failure(409, "SOURCE_COPY_PROTECTED", "对象清理条件已变化")
	}
	cleaner, ok := s.SourceStore.(client.SourceRetirer)
	if !ok {
		return client.ErrSourceCleanupUnsupported
	}
	if err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		_, err := s.OutboxEventsModel.FindOneOwnedForUpdateTx(ctx, tx, event.Id, event.AttemptCount)
		return err
	}); err != nil {
		return err
	}
	if err = cleaner.Retire(ctx, object.ObjectKey, object.ContentHash, object.SourceSize, object.Id); err != nil {
		return err
	}
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		current, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, event.TenantId, row.Id)
		if err != nil {
			return err
		}
		if current.SourceEpoch <= object.SourceEpoch || current.SourceKey == object.ObjectKey {
			return errForbidden
		}
		if _, err = s.OutboxEventsModel.FindOneOwnedForUpdateTx(ctx, tx, event.Id, event.AttemptCount); err != nil {
			return err
		}
		fresh, err := s.SourceObjectsModel.FindOneForUpdateTx(ctx, tx, event.TenantId, object.Id)
		if err != nil {
			return err
		}
		if fresh.Status == sourceObjectRetired {
			return nil
		}
		return s.SourceObjectsModel.UpdateStatusTx(ctx, tx, event.TenantId, object.Id, sourceObjectCleanupPending, sourceObjectRetired)
	})
}
