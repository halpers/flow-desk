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

const investigationRestoreOperation = "knowledge.source_investigation_restore"

func RestoreInvestigationRevision(ctx context.Context, s *svc.ServiceContext, req *types.RestoreInvestigationRevisionReq) (types.InvestigationRestoreData, error) {
	if _, err := knowledgeAdmin(ctx); err != nil {
		return types.InvestigationRestoreData{}, err
	}
	if !inventoryHashPattern.MatchString(req.ObjectRef) || req.Revision < 1 || req.ExpectedRevision < 1 || !req.Confirm {
		return types.InvestigationRestoreData{}, errArgument
	}
	result, err := idempotentAuthorized(ctx, s, investigationRestoreOperation, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.InvestigationRestoreData, error) {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return types.InvestigationRestoreData{}, err
		}
		initial, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if errors.Is(err, model.ErrNotFound) {
			return types.InvestigationRestoreData{}, errMissing
		}
		if err != nil {
			return types.InvestigationRestoreData{}, err
		}
		if err = investigationResourceTx(ctx, s, tx, actor, initial.ObjectKey); err != nil {
			return types.InvestigationRestoreData{}, err
		}
		// Share the ordinary editor's object lock and authorization -> object lock order.
		if err = s.SourceInvestigationsModel.LockObjectTx(ctx, tx, actor.TenantId, req.ObjectRef); err != nil {
			return types.InvestigationRestoreData{}, err
		}
		current, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return types.InvestigationRestoreData{}, err
		}
		if current.StoreId != initial.StoreId || current.ObjectKey != initial.ObjectKey {
			return types.InvestigationRestoreData{}, errForbidden
		}
		if current.Revision != req.ExpectedRevision {
			return types.InvestigationRestoreData{}, investigationChanged()
		}
		if req.Revision >= current.Revision {
			return types.InvestigationRestoreData{}, failure(409, "INVESTIGATION_RESTORE_NOT_OLDER", "请选择早于当前修订的已记录历史")
		}
		source, err := s.SourceInvestigationRevisionsModel.FindOneRevisionTx(ctx, tx, actor.TenantId, current.Id, req.Revision)
		if errors.Is(err, model.ErrNotFound) {
			return types.InvestigationRestoreData{}, errMissing
		}
		if err != nil {
			return types.InvestigationRestoreData{}, err
		}
		if err = s.SourceInvestigationsModel.UpdateNoteTx(ctx, tx, current, source.Status, source.Note, actor.Id); err != nil {
			return types.InvestigationRestoreData{}, err
		}
		saved, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return types.InvestigationRestoreData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: investigationRestoreOperation, ObjectType: "source_investigation", ObjectId: saved.Id, Source: "manual", Summary: fmt.Sprintf("Restored revision %d as new revision %d; no storage or registration changes", source.Revision, saved.Revision)}); err != nil {
			return types.InvestigationRestoreData{}, err
		}
		return types.InvestigationRestoreData{RestoredFromRevision: source.Revision, Investigation: investigationData(req.ObjectRef, saved)}, nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.InvestigationRestoreData) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if result.Investigation.ObjectRef != req.ObjectRef || result.RestoredFromRevision != req.Revision {
			return errForbidden
		}
		row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return err
		}
		return investigationResourceTx(ctx, s, tx, actor, row.ObjectKey)
	})
	if err != nil {
		return types.InvestigationRestoreData{}, err
	}
	return result, nil
}
