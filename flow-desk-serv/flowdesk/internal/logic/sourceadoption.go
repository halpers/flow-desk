package logic

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var errSourceAdoptionReadRequired = errors.New("source adoption requires external verification")

func sourceAdoptionScope(ctx context.Context, row *model.DocumentVersions, store string) string {
	actor := Current(ctx).User
	return strings.Join([]string{"source-adoption-v1", actor.TenantId, actor.Id, row.DocumentId, row.Id, row.ContentHash, store}, ":")
}
func sourceAdoptionReason(row *model.DocumentVersions, item types.SourceInventoryItem, enabled, originalVerified bool) string {
	switch {
	case !enabled:
		return "adoption_disabled"
	case item.RegisteredId != "":
		return "already_registered"
	case item.KeyKind == "unrecognized":
		return "unrecognized"
	case item.Size != row.SourceSize:
		return "size_mismatch"
	case row.SourceStorage != "postgres":
		return "restore_required"
	case !originalVerified:
		return "original_unverified"
	case item.SourceEpoch >= row.SourceEpoch:
		return "current_generation"
	default:
		return ""
	}
}
func openAdoptionSelection(ctx context.Context, s *svc.ServiceContext, row *model.DocumentVersions, store, token string) (inventoryPosition, error) {
	position, err := openInventoryCursor(s.Config.JwtSecret, sourceAdoptionScope(ctx, row, store), token, row.SourceEpoch)
	var business *BusinessError
	if errors.As(err, &business) && business.Code == "INVENTORY_CURSOR_EXPIRED" {
		return position, failure(410, "ADOPTION_SELECTION_EXPIRED", "登记选择已过期，请刷新巡检后重新核对")
	}
	return position, err
}

func AdoptSourceObject(ctx context.Context, s *svc.ServiceContext, req *types.SourceAdoptionReq) (types.SourceAdoptionData, error) {
	empty := types.SourceAdoptionData{}
	ref, err := hex.DecodeString(req.ObjectRef)
	if !validID(req.DocumentId) || !validID(req.VersionId) || len(ref) != 32 || err != nil || !req.Confirm || req.AdoptionToken == "" || len(req.AdoptionToken) > maximumSourceInventoryCursor || req.ExpectedSourceEpoch < 1 || req.ExpectedSourceEpoch > maximumSourceInventoryJSONInteger {
		return empty, errArgument
	}
	var prepared *model.DocumentVersions
	var key, store, verifiedAt string
	attempt := func() (types.SourceAdoptionData, error) {
		return idempotentAuthorized(ctx, s, "knowledge.source_adopt", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.SourceAdoptionData, error) {
			doc, err := sourceAdminDocumentTx(ctx, s, tx, req.DocumentId)
			if err != nil {
				return empty, err
			}
			row, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, doc.TenantId, req.VersionId)
			if err != nil {
				return empty, err
			}
			if row.DocumentId != doc.Id {
				return empty, errMissing
			}
			if !s.Config.FileStoreAdoptionEnabled || !s.Config.FileStoreInventoryEnabled {
				return empty, failure(409, "SOURCE_ADOPTION_DISABLED", "副本登记尚未启用，请联系运维")
			}
			if s.SourceStore == nil || s.Config.FileStoreProvider != "s3" {
				return empty, sourceUnavailable()
			}
			currentStore := s.SourceStore.ID()
			if row.SourceEpoch != req.ExpectedSourceEpoch {
				return empty, failure(409, "SOURCE_CHANGED", "原件代次已变化，请刷新巡检")
			}
			position, err := openAdoptionSelection(ctx, s, row, currentStore, req.AdoptionToken)
			if err != nil {
				return empty, err
			}
			if HashToken(currentStore+"\n"+position.After) != req.ObjectRef {
				return empty, errArgument
			}
			kind, objectEpoch := inventoryKeyKind(row, position.After)
			item := types.SourceInventoryItem{KeyKind: kind, SourceEpoch: objectEpoch, Size: row.SourceSize}
			if sourceAdoptionReason(row, item, true, validDatabaseOriginal(row)) != "" {
				return empty, failure(409, "SOURCE_ADOPTION_PROTECTED", "请恢复并核对数据库原件，只能登记标准旧副本")
			}
			if _, err = s.SourceObjectsModel.FindOneByStorageTx(ctx, tx, currentStore, position.After); err == nil {
				return empty, failure(409, "SOURCE_ALREADY_REGISTERED", "副本已有登记，请刷新巡检")
			}
			if !errors.Is(err, model.ErrNotFound) {
				return empty, err
			}
			count, err := s.SourceObjectsModel.CountRegisteredTx(ctx, tx, row.TenantId, row.Id)
			if err != nil {
				return empty, err
			}
			if count >= maxRegisteredSourceCopies {
				return empty, failure(429, "SOURCE_COPY_LIMIT", "归档副本已达到上限，请先核对旧副本")
			}
			if verifiedAt == "" {
				prepared, key, store = row, position.After, currentStore
				return empty, errSourceAdoptionReadRequired
			}
			if prepared.SourceEpoch != row.SourceEpoch || prepared.ContentHash != row.ContentHash || prepared.SourceSize != row.SourceSize || store != currentStore || key != position.After {
				return empty, failure(409, "SOURCE_CHANGED", "原件状态已变化，请刷新巡检")
			}
			registeredVersion := *row
			registeredVersion.SourceEpoch = objectEpoch
			if err = registerSourceObjectTx(ctx, s, tx, &registeredVersion, store, key); err != nil {
				return empty, err
			}
			object, err := s.SourceObjectsModel.FindOneByStorageTx(ctx, tx, store, key)
			if err != nil {
				return empty, err
			}
			if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: row.TenantId, ActorUserId: Current(ctx).User.Id, Operation: "knowledge.source_adopt", ObjectType: "source_object", ObjectId: object.Id, Source: "manual", Summary: "Historical copy registered after explicit confirmation and matching SHA256 read; database original preserved"}); err != nil {
				return empty, err
			}
			return types.SourceAdoptionData{ObjectId: object.Id, ObjectRef: req.ObjectRef, VersionId: row.Id, SourceEpoch: objectEpoch, VerifiedAt: verifiedAt}, nil
		}, func(ctx context.Context, tx sqlx.Session, result *types.SourceAdoptionData) error {
			row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
			if err != nil {
				return err
			}
			object, err := s.SourceObjectsModel.FindOneForUpdateTx(ctx, tx, row.TenantId, result.ObjectId)
			if err != nil {
				return err
			}
			if object.VersionId != row.Id || result.VersionId != row.Id || result.ObjectRef != req.ObjectRef || HashToken(object.StoreId+"\n"+object.ObjectKey) != result.ObjectRef || object.SourceEpoch != result.SourceEpoch {
				return errForbidden
			}
			return nil
		})
	}
	// A replay returns before external I/O. A fresh request deliberately rolls back
	// its first transaction, verifies outside locks, then repeats all guards to commit.
	result, err := attempt()
	if !errors.Is(err, errSourceAdoptionReadRequired) {
		return result, err
	}
	raw, err := s.SourceStore.Read(ctx, key, maxPDFBytes)
	if errors.Is(err, client.ErrSourceMissing) {
		return empty, failure(404, "SOURCE_COPY_MISSING", "副本已不存在，请刷新巡检")
	}
	if err != nil {
		return empty, sourceUnavailable()
	}
	if int64(len(raw)) != prepared.SourceSize || HashToken(string(raw)) != prepared.ContentHash {
		return empty, sourceIntegrity()
	}
	verifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return attempt()
}
