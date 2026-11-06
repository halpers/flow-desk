package logic

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const investigationArchiveOperation = "knowledge.source_investigation_archive"
const investigationHistoryKeepLatest int64 = 3
const investigationHistoryMinimumAge = 30 * 24 * time.Hour

func investigationArchiveBlock(current *model.SourceInvestigations, row *model.SourceInvestigationRevisions, archived bool, now time.Time) string {
	if archived {
		return "already_archived"
	}
	if current.Status != investigationClosed {
		return "investigation_active"
	}
	if row.Revision > current.Revision-investigationHistoryKeepLatest {
		return "keep_latest"
	}
	if row.ChangedAt.After(now.Add(-investigationHistoryMinimumAge)) {
		return "minimum_age"
	}
	return ""
}
func ArchiveInvestigationRevision(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveInvestigationRevisionReq) (types.InvestigationArchiveData, error) {
	if _, err := knowledgeAdmin(ctx); err != nil {
		return types.InvestigationArchiveData{}, err
	}
	if !inventoryHashPattern.MatchString(req.ObjectRef) || req.Revision < 1 || req.ExpectedRevision < 1 || !req.Confirm {
		return types.InvestigationArchiveData{}, errArgument
	}
	result, err := idempotentAuthorized(ctx, s, investigationArchiveOperation, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.InvestigationArchiveData, error) {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return types.InvestigationArchiveData{}, err
		}
		initial, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if errors.Is(err, model.ErrNotFound) {
			return types.InvestigationArchiveData{}, errMissing
		}
		if err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if err = investigationResourceTx(ctx, s, tx, actor, initial.ObjectKey); err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if err = s.SourceInvestigationsModel.LockObjectTx(ctx, tx, actor.TenantId, req.ObjectRef); err != nil {
			return types.InvestigationArchiveData{}, err
		}
		// The row lock serializes tier changes with FOR SHARE history reads.
		current, err := s.SourceInvestigationsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if current.StoreId != initial.StoreId || current.ObjectKey != initial.ObjectKey {
			return types.InvestigationArchiveData{}, errForbidden
		}
		if current.Revision != req.ExpectedRevision {
			return types.InvestigationArchiveData{}, investigationChanged()
		}
		_, err = s.SourceInvestigationRevisionArchivesModel.FindOneRevisionTx(ctx, tx, actor.TenantId, current.Id, req.Revision)
		if err == nil {
			return types.InvestigationArchiveData{}, failure(409, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED", "该修订已归档，请刷新编辑历史")
		}
		if !errors.Is(err, model.ErrNotFound) {
			return types.InvestigationArchiveData{}, err
		}
		source, err := s.SourceInvestigationRevisionsModel.FindOneRevisionTx(ctx, tx, actor.TenantId, current.Id, req.Revision)
		if errors.Is(err, model.ErrNotFound) {
			return types.InvestigationArchiveData{}, errMissing
		}
		if err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if reason := investigationArchiveBlock(current, source, false, time.Now()); reason != "" {
			return types.InvestigationArchiveData{}, failure(409, "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED", "该修订受调查状态、最近修订或最短保留期限保护")
		}
		if err = s.SourceInvestigationRevisionArchivesModel.InsertSnapshotTx(ctx, tx, source, actor.Id); err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if err = s.SourceInvestigationRevisionsModel.DeleteArchivedTx(ctx, tx, actor.TenantId, source.Id, source.Revision); err != nil {
			return types.InvestigationArchiveData{}, err
		}
		archived, err := s.SourceInvestigationRevisionArchivesModel.FindOneRevisionTx(ctx, tx, actor.TenantId, current.Id, req.Revision)
		if err != nil {
			return types.InvestigationArchiveData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: investigationArchiveOperation, ObjectType: "source_investigation", ObjectId: current.Id, Source: "manual", Summary: fmt.Sprintf("Archived revision %d with complete history retained; current revision %d unchanged", source.Revision, current.Revision)}); err != nil {
			return types.InvestigationArchiveData{}, err
		}
		return types.InvestigationArchiveData{ObjectRef: req.ObjectRef, Revision: source.Revision, ArchivedAt: archived.ArchivedAt.UTC().Format(time.RFC3339Nano)}, nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.InvestigationArchiveData) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if result.ObjectRef != req.ObjectRef || result.Revision != req.Revision {
			return errForbidden
		}
		row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return err
		}
		return investigationResourceTx(ctx, s, tx, actor, row.ObjectKey)
	})
	if err != nil {
		return types.InvestigationArchiveData{}, err
	}
	return result, nil
}
