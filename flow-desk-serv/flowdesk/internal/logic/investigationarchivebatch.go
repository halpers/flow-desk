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

const maximumInvestigationArchiveBatch = 50
const investigationArchiveBatchOperation = "knowledge.source_investigation_archive_batch"
const investigationArchivePreviewOperation = "knowledge.source_investigation_archive_preview"

func validateInvestigationArchiveSelection(ref string, expected int64, revisions []int64) error {
	if !inventoryHashPattern.MatchString(ref) || expected < 1 || len(revisions) < 1 || len(revisions) > maximumInvestigationArchiveBatch {
		return errArgument
	}
	previous := int64(0)
	for _, revision := range revisions {
		if revision <= previous {
			return errArgument
		}
		previous = revision
	}
	return nil
}

// Both paths retain the authorization -> resource -> parent lock order.
func investigationArchiveCurrentTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, ref string, expected int64, write bool) (*model.Users, *model.SourceInvestigations, error) {
	actor, err := tenantInventoryActorTx(ctx, s, tx)
	if err != nil {
		return nil, nil, err
	}
	initial, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, ref)
	if errors.Is(err, model.ErrNotFound) {
		return nil, nil, errMissing
	}
	if err != nil {
		return nil, nil, err
	}
	if err = investigationResourceTx(ctx, s, tx, actor, initial.ObjectKey); err != nil {
		return nil, nil, err
	}
	var current *model.SourceInvestigations
	if write {
		if err = s.SourceInvestigationsModel.LockObjectTx(ctx, tx, actor.TenantId, ref); err != nil {
			return nil, nil, err
		}
		current, err = s.SourceInvestigationsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, ref)
	} else {
		current, err = s.SourceInvestigationsModel.FindOneForShareTx(ctx, tx, actor.TenantId, ref)
	}
	if err != nil {
		return nil, nil, err
	}
	if current.StoreId != initial.StoreId || current.ObjectKey != initial.ObjectKey {
		return nil, nil, errForbidden
	}
	if current.Revision != expected {
		return nil, nil, investigationChanged()
	}
	return actor, current, nil
}
func investigationArchiveSelectionTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, current *model.SourceInvestigations, revisions []int64) ([]model.SourceInvestigationRevisions, error) {
	rows, err := s.SourceInvestigationRevisionsModel.ListSelectedTx(ctx, tx, current.TenantId, current.Id, revisions)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(revisions) {
		return nil, errMissing
	}
	archives, err := s.SourceInvestigationRevisionArchivesModel.ListRevisionsTx(ctx, tx, current.TenantId, current.Id, revisions)
	if err != nil {
		return nil, err
	}
	if len(archives) > 0 {
		return nil, failure(409, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED", "选中的修订已有归档，请刷新编辑历史")
	}
	observed := time.Now()
	for index, row := range rows {
		if row.Revision != revisions[index] {
			return nil, errMissing
		}
		if investigationArchiveBlock(current, &row, false, observed) != "" {
			return nil, failure(409, "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED", "选中的修订受调查状态、最近修订或最短保留期限保护")
		}
	}
	return rows, nil
}
func PreviewInvestigationArchive(ctx context.Context, s *svc.ServiceContext, req *types.PreviewInvestigationArchiveReq) (types.InvestigationArchivePreviewData, error) {
	if _, err := knowledgeAdmin(ctx); err != nil {
		return types.InvestigationArchivePreviewData{}, err
	}
	if err := validateInvestigationArchiveSelection(req.ObjectRef, req.ExpectedRevision, req.Revisions); err != nil {
		return types.InvestigationArchivePreviewData{}, err
	}
	result := types.InvestigationArchivePreviewData{ObjectRef: req.ObjectRef, CurrentRevision: req.ExpectedRevision, MaximumItems: maximumInvestigationArchiveBatch, Items: []types.InvestigationRevisionItem{}}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, current, err := investigationArchiveCurrentTx(ctx, s, tx, req.ObjectRef, req.ExpectedRevision, false)
		if err != nil {
			return err
		}
		rows, err := investigationArchiveSelectionTx(ctx, s, tx, current, req.Revisions)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result.Items = append(result.Items, types.InvestigationRevisionItem{Revision: row.Revision, Status: row.Status, Note: row.Note, ChangedBy: row.ChangedBy, ChangedAt: row.ChangedAt.UTC().Format(time.RFC3339Nano), IsBaseline: row.IsBaseline, CanArchive: true})
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: investigationArchivePreviewOperation, ObjectType: "source_investigation", ObjectId: current.Id, Source: "manual", Summary: fmt.Sprintf("Previewed %d selected revision snapshots at current revision %d", len(rows), current.Revision)})
	})
	if err != nil {
		return types.InvestigationArchivePreviewData{}, err
	}
	return result, nil
}
func ArchiveInvestigationBatch(ctx context.Context, s *svc.ServiceContext, req *types.ArchiveInvestigationBatchReq) (types.InvestigationArchiveBatchData, error) {
	if _, err := knowledgeAdmin(ctx); err != nil {
		return types.InvestigationArchiveBatchData{}, err
	}
	if err := validateInvestigationArchiveSelection(req.ObjectRef, req.ExpectedRevision, req.Revisions); err != nil {
		return types.InvestigationArchiveBatchData{}, err
	}
	if !req.Confirm {
		return types.InvestigationArchiveBatchData{}, errArgument
	}
	result, err := idempotentAuthorized(ctx, s, investigationArchiveBatchOperation, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.InvestigationArchiveBatchData, error) {
		actor, current, err := investigationArchiveCurrentTx(ctx, s, tx, req.ObjectRef, req.ExpectedRevision, true)
		if err != nil {
			return types.InvestigationArchiveBatchData{}, err
		}
		rows, err := investigationArchiveSelectionTx(ctx, s, tx, current, req.Revisions)
		if err != nil {
			return types.InvestigationArchiveBatchData{}, err
		}
		receipt := types.InvestigationArchiveBatchData{ObjectRef: req.ObjectRef, Items: []types.InvestigationArchiveData{}}
		for _, row := range rows {
			if err = s.SourceInvestigationRevisionArchivesModel.InsertSnapshotTx(ctx, tx, &row, actor.Id); err != nil {
				return types.InvestigationArchiveBatchData{}, err
			}
			if err = s.SourceInvestigationRevisionsModel.DeleteArchivedTx(ctx, tx, actor.TenantId, row.Id, row.Revision); err != nil {
				return types.InvestigationArchiveBatchData{}, err
			}
			archived, err := s.SourceInvestigationRevisionArchivesModel.FindOneRevisionTx(ctx, tx, actor.TenantId, current.Id, row.Revision)
			if err != nil {
				return types.InvestigationArchiveBatchData{}, err
			}
			receipt.Items = append(receipt.Items, types.InvestigationArchiveData{ObjectRef: req.ObjectRef, Revision: row.Revision, ArchivedAt: archived.ArchivedAt.UTC().Format(time.RFC3339Nano)})
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: investigationArchiveBatchOperation, ObjectType: "source_investigation", ObjectId: current.Id, Source: "manual", Summary: fmt.Sprintf("Archived %d selected revision snapshots; current revision %d unchanged", len(rows), current.Revision)}); err != nil {
			return types.InvestigationArchiveBatchData{}, err
		}
		return receipt, nil
	}, func(ctx context.Context, tx sqlx.Session, receipt *types.InvestigationArchiveBatchData) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if receipt.ObjectRef != req.ObjectRef || len(receipt.Items) != len(req.Revisions) {
			return errForbidden
		}
		for index, item := range receipt.Items {
			if item.ObjectRef != req.ObjectRef || item.Revision != req.Revisions[index] {
				return errForbidden
			}
		}
		current, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return err
		}
		return investigationResourceTx(ctx, s, tx, actor, current.ObjectKey)
	})
	if err != nil {
		return types.InvestigationArchiveBatchData{}, err
	}
	return result, nil
}
