package logic

import (
	"context"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const defaultSourceHistoryPage int64 = 20
const maximumSourceHistoryPage int64 = 50

func sourceHistoryItem(row *model.SourceCopyHistoryRow) types.SourceCopyHistoryItem {
	item := types.SourceCopyHistoryItem{Id: row.Id, SourceEpoch: row.SourceEpoch, SourceSize: row.SourceSize, Status: row.Status, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), MetadataArchived: row.MetadataArchived, CanArchive: row.ArchiveBlockReason == "", ArchiveBlockReason: row.ArchiveBlockReason}
	if row.RetiredAt.Valid {
		item.RetiredAt = row.RetiredAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if row.ArchivedAt.Valid {
		item.ArchivedAt = row.ArchivedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return item
}
func ListSourceCopyHistory(ctx context.Context, s *svc.ServiceContext, req *types.SourceCopyHistoryReq) (types.SourceCopyHistoryPage, error) {
	result := types.SourceCopyHistoryPage{VersionId: req.VersionId, Items: []types.SourceCopyHistoryItem{}}
	limit := req.Limit
	if limit == 0 {
		limit = defaultSourceHistoryPage
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || limit < 1 || limit > maximumSourceHistoryPage {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		actor := Current(ctx).User
		scope := strings.Join([]string{"source-copy-history-v1", actor.TenantId, actor.Id, req.DocumentId, req.VersionId}, ":")
		position, err := decodeCursor(req.Cursor, scope)
		if err != nil {
			return err
		}
		before, beforeID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), "ffffffff-ffff-ffff-ffff-ffffffffffff"
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
		} else if req.Cursor != "" {
			return errArgument
		}
		rows, err := s.SourceObjectsModel.ListHistoryTx(ctx, tx, row.TenantId, row.Id, before, beforeID, limit+1)
		if err != nil {
			return err
		}
		result.SourceEpoch = row.SourceEpoch
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for i := range rows {
			result.Items = append(result.Items, sourceHistoryItem(&rows[i]))
		}
		if result.HasMore {
			last := rows[len(rows)-1]
			result.NextCursor = encodeCursor(scope, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"|"+last.Id)
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.SourceCopyHistoryPage{}, err
	}
	return result, nil
}
func ArchiveSourceMetadata(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveSourceMetadataReq) (types.SourceMetadataArchiveData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.SourceMetadataArchiveData{}, err
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || !validID(req.ObjectId) || !req.Confirm || req.ExpectedSourceEpoch < 1 {
		return types.SourceMetadataArchiveData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.source_metadata_archive", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.SourceMetadataArchiveData, error) {
		doc, err := s.DocumentsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.DocumentId)
		if err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		row, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.VersionId)
		if err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if row.DocumentId != doc.Id {
			return types.SourceMetadataArchiveData{}, errMissing
		}
		if row.SourceEpoch != req.ExpectedSourceEpoch {
			return types.SourceMetadataArchiveData{}, failure(409, "SOURCE_CHANGED", "原件代次已变化，请刷新副本历史")
		}
		object, err := s.SourceObjectsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.ObjectId)
		if err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if object.VersionId != row.Id {
			return types.SourceMetadataArchiveData{}, errMissing
		}
		history, err := s.SourceObjectsModel.FindHistoryItemTx(ctx, tx, actor.TenantId, row.Id, object.Id)
		if err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if history.ArchiveBlockReason != "" {
			return types.SourceMetadataArchiveData{}, failure(409, "SOURCE_METADATA_ARCHIVE_PROTECTED", "登记仍受保护或已经归档，请刷新副本历史")
		}
		archive := &model.SourceObjectArchives{Id: object.Id, TenantId: object.TenantId, VersionId: object.VersionId, SourceEpoch: object.SourceEpoch, StoreId: object.StoreId, ObjectKey: object.ObjectKey, ContentHash: object.ContentHash, SourceSize: object.SourceSize, Status: sourceObjectRetired, CreatedAt: object.CreatedAt, RetiredAt: object.RetiredAt.Time, ArchivedBy: actor.Id}
		if err = s.SourceObjectArchivesModel.InsertTx(ctx, tx, archive); err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if err = s.SourceObjectsModel.DeleteRetiredTx(ctx, tx, actor.TenantId, object.Id); err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_metadata_archive", ObjectType: "source_object", ObjectId: object.Id, Source: "manual", Summary: "Retired registration moved to history; identity, storage marker, receipts and deduplication preserved"}); err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		stored, err := s.SourceObjectArchivesModel.FindOneByTenantIdIdTx(ctx, tx, actor.TenantId, object.Id)
		if err != nil {
			return types.SourceMetadataArchiveData{}, err
		}
		return types.SourceMetadataArchiveData{ObjectId: stored.Id, VersionId: stored.VersionId, ArchivedAt: stored.ArchivedAt.UTC().Format(time.RFC3339Nano)}, nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.SourceMetadataArchiveData) error {
		if result.ObjectId != req.ObjectId || result.VersionId != req.VersionId {
			return errForbidden
		}
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		stored, err := s.SourceObjectArchivesModel.FindOneByTenantIdIdTx(ctx, tx, row.TenantId, result.ObjectId)
		if err != nil {
			return err
		}
		if stored.VersionId != row.Id {
			return errForbidden
		}
		return nil
	})
}
