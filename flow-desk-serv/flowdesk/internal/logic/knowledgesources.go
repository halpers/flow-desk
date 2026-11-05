package logic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func sourceUnavailable() error {
	return failure(503, "SOURCE_UNAVAILABLE", "原文件暂时不可用，请联系管理员核对存储或备份")
}
func sourceIntegrity() error {
	return failure(503, "SOURCE_INTEGRITY_FAILED", "原文件完整性校验失败，请联系管理员恢复备份")
}
func readOriginal(ctx context.Context, s *svc.ServiceContext, row *model.DocumentVersions) ([]byte, error) {
	raw := row.SourceBytes
	if row.SourceFormat != "pdf" {
		raw = []byte(row.Content)
	}
	if row.SourceStorage == "s3" {
		if s.SourceStore == nil || s.SourceStore.ID() != row.SourceStoreId {
			return nil, sourceUnavailable()
		}
		var err error
		raw, err = s.SourceStore.Read(ctx, row.SourceKey, maxPDFBytes)
		if err != nil {
			return nil, sourceUnavailable()
		}
	}
	if int64(len(raw)) != row.SourceSize || HashToken(string(raw)) != row.ContentHash {
		return nil, sourceIntegrity()
	}
	return raw, nil
}
func sourceAdminVersionTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, document, version string) (*model.DocumentVersions, error) {
	actor, err := operationsViewerTx(ctx, s, tx)
	if err != nil {
		return nil, err
	}
	if !permitted(actor, "knowledge:manage") {
		return nil, errForbidden
	}
	row, err := s.DocumentVersionsModel.FindOneForShareTx(ctx, tx, actor.TenantId, version)
	if err != nil {
		return nil, err
	}
	if row.DocumentId != document {
		return nil, errMissing
	}
	return row, nil
}
func sourceAdminVersion(ctx context.Context, s *svc.ServiceContext, document, version string) (*model.DocumentVersions, error) {
	if !validID(document) || !validID(version) {
		return nil, errArgument
	}
	var row *model.DocumentVersions
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		row, err = sourceAdminVersionTx(ctx, s, tx, document, version)
		return err
	})
	return row, err
}
func DownloadOriginalSource(ctx context.Context, s *svc.ServiceContext, req *types.VersionPagesReq) (types.OriginalSourceData, error) {
	var result types.OriginalSourceData
	row, err := sourceAdminVersion(ctx, s, req.DocumentId, req.VersionId)
	if err != nil {
		return result, err
	}
	raw, err := readOriginal(ctx, s, row)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		current, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		if current.ContentHash != row.ContentHash {
			return sourceIntegrity()
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: row.TenantId, ActorUserId: Current(ctx).User.Id, Operation: "knowledge.source_download", ObjectType: "document_version", ObjectId: row.Id, Source: "manual", Summary: "Original file downloaded after authorization and integrity checks"})
	})
	if err != nil {
		return result, err
	}
	return types.OriginalSourceData{Filename: row.Filename, ContentHash: row.ContentHash, SourceFormat: row.SourceFormat, ContentBase64: base64.StdEncoding.EncodeToString(raw)}, nil
}

type sourceArchivePayload struct {
	VersionId string `json:"version_id"`
	Epoch     int64  `json:"source_epoch"`
}

func enqueueSourceArchiveTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.DocumentVersions) error {
	dispatch := uuid.NewSHA1(uuid.MustParse(row.Id), []byte(fmt.Sprintf("knowledge.archive:%d", row.SourceEpoch))).String()
	return s.OutboxEventsModel.UpsertArchiveTx(ctx, tx, &model.OutboxEvents{Id: uuid.NewString(), TenantId: row.TenantId, AggregateId: row.DocumentId, DispatchId: dispatch, Payload: marshal(sourceArchivePayload{row.Id, row.SourceEpoch})})
}
func RequestSourceArchive(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveSourceReq) (types.VersionData, error) {
	if _, err := sourceAdminVersion(ctx, s, req.DocumentId, req.VersionId); err != nil {
		return types.VersionData{}, err
	}
	if s.SourceStore == nil || s.Config.FileStoreProvider != "s3" {
		return types.VersionData{}, failure(409, "SOURCE_ARCHIVE_DISABLED", "当前未启用原文件归档")
	}
	return idempotentAuthorized(ctx, s, "knowledge.source_archive", req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.VersionData, error) {
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return types.VersionData{}, err
		}
		if row.SourceFormat != "pdf" {
			return types.VersionData{}, errArgument
		}
		if row.SourceStorage == "postgres" {
			if err = enqueueSourceArchiveTx(ctx, s, tx, row); err != nil {
				return types.VersionData{}, err
			}
		}
		err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: row.TenantId, ActorUserId: Current(ctx).User.Id, Operation: "knowledge.source_archive", ObjectType: "document_version", ObjectId: row.Id, Source: "manual", Summary: "Original PDF archive requested"})
		return versionData(row), err
	}, func(ctx context.Context, tx sqlx.Session, result *types.VersionData) error {
		row, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err == nil {
			*result = versionData(row)
		}
		return err
	})
}
func ArchiveOriginalSource(ctx context.Context, s *svc.ServiceContext, event *model.OutboxEvents) error {
	var payload sourceArchivePayload
	if json.Unmarshal([]byte(event.Payload), &payload) != nil || !validID(payload.VersionId) || payload.Epoch < 0 {
		return errArgument
	}
	row, err := s.DocumentVersionsModel.FindOneByTenantIdId(ctx, event.TenantId, payload.VersionId)
	if err != nil {
		return err
	}
	if row.DocumentId != event.AggregateId || row.SourceFormat != "pdf" {
		return errArgument
	}
	if row.SourceEpoch != payload.Epoch || row.SourceStorage == "s3" {
		return nil
	}
	if s.SourceStore == nil || s.Config.FileStoreProvider != "s3" {
		return sourceUnavailable()
	}
	raw, err := readOriginal(ctx, s, row)
	if err != nil {
		return err
	}
	key := "knowledge/" + row.TenantId + "/" + row.Id + "/" + row.ContentHash + ".pdf"
	stored, err := s.SourceStore.Read(ctx, key, maxPDFBytes)
	if errors.Is(err, client.ErrSourceMissing) {
		if err = s.SourceStore.PutIfAbsent(ctx, key, raw); err != nil {
			return sourceUnavailable()
		}
		stored, err = s.SourceStore.Read(ctx, key, maxPDFBytes)
	}
	if err != nil {
		return sourceUnavailable()
	}
	if len(stored) != len(raw) || HashToken(string(stored)) != row.ContentHash {
		return sourceIntegrity()
	}
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		current, err := s.DocumentVersionsModel.FindOneByJobIdForUpdateTx(ctx, tx, row.JobId)
		if err != nil {
			return err
		}
		if _, err := s.OutboxEventsModel.FindOneOwnedForUpdateTx(ctx, tx, event.Id, event.AttemptCount); err != nil {
			return err
		}
		if current.SourceEpoch != payload.Epoch || current.SourceStorage == "s3" {
			return nil
		}
		return s.DocumentVersionsModel.UpdateArchivedSourceTx(ctx, tx, current, s.SourceStore.ID(), key)
	})
}
func RestoreOriginalSource(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveSourceReq) (types.VersionData, error) {
	row, err := sourceAdminVersion(ctx, s, req.DocumentId, req.VersionId)
	if err != nil {
		return types.VersionData{}, err
	}
	raw, err := readOriginal(ctx, s, row)
	if err != nil {
		return types.VersionData{}, err
	}
	return idempotentAuthorized(ctx, s, "knowledge.source_restore", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.VersionData, error) {
		current, err := s.DocumentVersionsModel.FindOneByJobIdForUpdateTx(ctx, tx, row.JobId)
		if err != nil {
			return types.VersionData{}, err
		}
		if _, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId); err != nil {
			return types.VersionData{}, err
		}
		if current.SourceStorage == "s3" {
			if current.SourceEpoch != row.SourceEpoch {
				return types.VersionData{}, failure(409, "SOURCE_CHANGED", "文件存储状态已变化，请刷新")
			}
			if err = s.DocumentVersionsModel.UpdateRestoredSourceTx(ctx, tx, current, raw); err != nil {
				return types.VersionData{}, err
			}
			current.SourceStorage = "postgres"
			current.SourceBytes = raw
			current.SourceEpoch++
			if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: row.TenantId, ActorUserId: Current(ctx).User.Id, Operation: "knowledge.source_restore", ObjectType: "document_version", ObjectId: row.Id, Source: "manual", Summary: "Original PDF restored into PostgreSQL; stale archive generation invalidated"}); err != nil {
				return types.VersionData{}, err
			}
		}
		return versionData(current), nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.VersionData) error {
		current, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err == nil {
			*result = versionData(current)
		}
		return err
	})
}
