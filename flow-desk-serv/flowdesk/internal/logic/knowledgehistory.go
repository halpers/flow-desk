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

const maximumHistoryPage int64 = 50

func historyItem(row *model.VersionHistoryRow) types.VersionHistoryItem {
	item := types.VersionHistoryItem{Id: row.Id, DocumentId: row.DocumentId, Filename: row.Filename,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), CleanupState: row.CleanupState, ContentHash: row.ContentHash,
		IndexStatus: row.IndexStatus, ChunkCount: row.ChunkCount, ErrorCode: row.ErrorCode, ParserVersion: row.ParserVersion,
		OcrMode: row.OcrMode, PdfLayout: row.PdfLayout, SourceFormat: row.SourceFormat, SourceSize: row.SourceSize,
		MetadataArchived: row.MetadataArchived, CanArchive: row.ArchiveBlockReason == "", ArchiveBlockReason: row.ArchiveBlockReason}
	if row.ArchivedAt.Valid {
		item.ArchivedAt = row.ArchivedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return item
}

func ListVersionHistory(ctx context.Context, s *svc.ServiceContext, req *types.VersionHistoryReq) (types.VersionHistoryPage, error) {
	result := types.VersionHistoryPage{Items: []types.VersionHistoryItem{}}
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}
	if !validID(req.DocumentId) || limit < 1 || limit > maximumHistoryPage {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		doc, err := sourceAdminDocumentTx(ctx, s, tx, req.DocumentId)
		if err != nil {
			return err
		}
		actor := Current(ctx).User
		scope := strings.Join([]string{"version-history-v1", actor.TenantId, actor.Id, doc.Id}, ":")
		position, err := decodeCursor(req.Cursor, scope)
		if err != nil {
			return err
		}
		before := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
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
		} else if req.Cursor != "" {
			return errArgument
		}
		rows, err := s.DocumentVersionsModel.ListHistoryTx(ctx, tx, doc.TenantId, doc.Id, before, beforeID, limit+1)
		if err != nil {
			return err
		}
		result.DocumentVersion = doc.Version
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for i := range rows {
			result.Items = append(result.Items, historyItem(&rows[i]))
		}
		if result.HasMore {
			last := rows[len(rows)-1]
			result.NextCursor = encodeCursor(scope, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"|"+last.Id)
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	return result, err
}

func ArchiveVersionMetadata(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveMetadataReq) (types.MetadataArchiveData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.MetadataArchiveData{}, err
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || !req.Confirm || req.ExpectedDocumentVersion < 1 {
		return types.MetadataArchiveData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.metadata_archive", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.MetadataArchiveData, error) {
		// Same document -> ACL -> version lock order as activation and content cleanup.
		doc, err := s.DocumentsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.DocumentId)
		if err != nil {
			return types.MetadataArchiveData{}, err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return types.MetadataArchiveData{}, err
		}
		if doc.Version != req.ExpectedDocumentVersion {
			return types.MetadataArchiveData{}, failure(409, "VERSION_CONFLICT", "文档状态已变化，请刷新历史记录")
		}
		row, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.VersionId)
		if err != nil {
			return types.MetadataArchiveData{}, err
		}
		if row.DocumentId != doc.Id {
			return types.MetadataArchiveData{}, errMissing
		}
		history, err := s.DocumentVersionsModel.FindHistoryVersionTx(ctx, tx, actor.TenantId, doc.Id, row.Id)
		if err != nil {
			return types.MetadataArchiveData{}, err
		}
		if history.ArchiveBlockReason != "" {
			return types.MetadataArchiveData{}, failure(409, "METADATA_ARCHIVE_PROTECTED", "版本仍受保护或元数据已经归档，请刷新历史记录")
		}
		archive := &model.VersionMetadataArchives{Id: row.Id, TenantId: actor.TenantId, DocumentId: doc.Id, Filename: row.Filename,
			ParserVersion: row.ParserVersion, ErrorCode: row.ErrorCode, ChunkCount: row.ChunkCount, OcrMode: row.OcrMode, PdfLayout: row.PdfLayout, ArchivedBy: actor.Id}
		if err = s.VersionMetadataArchivesModel.InsertTx(ctx, tx, archive); err != nil {
			return types.MetadataArchiveData{}, err
		}
		if err = s.DocumentVersionsModel.UpdateMetadataArchivedTx(ctx, tx, actor.TenantId, row.Id); err != nil {
			return types.MetadataArchiveData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id,
			Operation: "knowledge.metadata_archive", ObjectType: "document_version", ObjectId: row.Id, Source: "manual",
			Summary: "Purged version metadata archived; identity, callback fences, receipts and audit preserved"}); err != nil {
			return types.MetadataArchiveData{}, err
		}
		stored, err := s.VersionMetadataArchivesModel.FindOneByTenantIdIdTx(ctx, tx, actor.TenantId, row.Id)
		if err != nil {
			return types.MetadataArchiveData{}, err
		}
		return types.MetadataArchiveData{VersionId: row.Id, DocumentId: doc.Id, ArchivedAt: stored.CreatedAt.UTC().Format(time.RFC3339Nano)}, nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.MetadataArchiveData) error {
		if result.DocumentId != req.DocumentId || result.VersionId != req.VersionId {
			return errForbidden
		}
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		if !row.MetadataArchived {
			return errMissing
		}
		_, err = s.VersionMetadataArchivesModel.FindOneByTenantIdIdTx(ctx, tx, actor.TenantId, row.Id)
		return err
	})
}
