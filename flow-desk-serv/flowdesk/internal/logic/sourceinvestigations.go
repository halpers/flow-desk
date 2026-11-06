package logic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maximumInvestigationNote = 2000
const investigationOpen = "open"
const investigationActive = "investigating"
const investigationClosed = "closed"

func investigationStatuses() []string {
	return []string{investigationOpen, investigationActive, investigationClosed}
}
func investigationScope(actor *model.Users, store string) string {
	return strings.Join([]string{"source-investigation-selection-v1", actor.TenantId, actor.Id, store}, ":")
}
func investigationData(ref string, row *model.SourceInvestigations) types.SourceInvestigationData {
	data := types.SourceInvestigationData{ObjectRef: ref, Status: investigationOpen, AllowedStatuses: investigationStatuses()}
	if row != nil {
		data.Revision = row.Revision
		data.Status = row.Status
		data.Note = row.Note
		data.UpdatedBy = row.UpdatedBy
		data.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return data
}

// Name hints enforce a known resource's current ACL, never prove ownership.
func investigationResourceTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, key string) error {
	if !strings.HasPrefix(key, "knowledge/"+actor.TenantId+"/") {
		return errForbidden
	}
	hint := tenantInventoryVersionHint(actor.TenantId, key)
	if hint == "" {
		return nil
	}
	row, err := s.DocumentVersionsModel.FindInventoryIdentityTx(ctx, tx, actor.TenantId, hint)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = sourceAdminDocumentTx(ctx, s, tx, row.DocumentId)
	return err
}
func GetSourceInvestigation(ctx context.Context, s *svc.ServiceContext, req *types.SourceInvestigationReq) (types.SourceInvestigationData, error) {
	if !inventoryHashPattern.MatchString(req.ObjectRef) {
		return types.SourceInvestigationData{}, errArgument
	}
	var result types.SourceInvestigationData
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if errors.Is(err, model.ErrNotFound) {
			row = nil
		} else if err != nil {
			return err
		}
		if row != nil {
			if err = investigationResourceTx(ctx, s, tx, actor, row.ObjectKey); err != nil {
				return err
			}
		}
		result = investigationData(req.ObjectRef, row)
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.SourceInvestigationData{}, err
	}
	return result, nil
}
func SaveSourceInvestigation(ctx context.Context, s *svc.ServiceContext, req *types.SaveSourceInvestigationReq) (types.SourceInvestigationData, error) {
	if _, err := knowledgeAdmin(ctx); err != nil {
		return types.SourceInvestigationData{}, err
	}
	req.Note = strings.TrimSpace(req.Note)
	invalidControl := strings.IndexFunc(req.Note, func(value rune) bool { return unicode.IsControl(value) && value != '\n' && value != '\t' }) >= 0
	if !inventoryHashPattern.MatchString(req.ObjectRef) || req.ExpectedRevision < 0 || !req.Confirm || !slices.Contains(investigationStatuses(), req.Status) || len(req.SelectionToken) > maximumSourceInventoryCursor || utf8.RuneCountInString(req.Note) < 1 || utf8.RuneCountInString(req.Note) > maximumInvestigationNote || invalidControl {
		return types.SourceInvestigationData{}, errArgument
	}
	result, err := idempotentAuthorized(ctx, s, "knowledge.source_investigate", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.SourceInvestigationData, error) {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return types.SourceInvestigationData{}, err
		}
		initial, lookupErr := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		var store, key string
		if errors.Is(lookupErr, model.ErrNotFound) {
			if !s.Config.FileStoreInventoryEnabled || !s.Config.FileStoreTenantInventoryEnabled {
				return types.SourceInvestigationData{}, failure(409, "TENANT_SOURCE_INVENTORY_DISABLED", "首次调查需要启用租户巡检并刷新选择")
			}
			if s.SourceStore == nil {
				return types.SourceInvestigationData{}, sourceUnavailable()
			}
			if req.SelectionToken == "" {
				return types.SourceInvestigationData{}, errArgument
			}
			store = s.SourceStore.ID()
			selection, selectionErr := openInventoryCursor(s.Config.JwtSecret, investigationScope(actor, store), req.SelectionToken, 0)
			if selectionErr != nil {
				return types.SourceInvestigationData{}, selectionErr
			}
			key = selection.After
			if HashToken(store+"\n"+key) != req.ObjectRef {
				return types.SourceInvestigationData{}, errArgument
			}
		} else if lookupErr != nil {
			return types.SourceInvestigationData{}, lookupErr
		} else {
			store, key = initial.StoreId, initial.ObjectKey
		}
		if err = investigationResourceTx(ctx, s, tx, actor, key); err != nil {
			return types.SourceInvestigationData{}, err
		}
		if err = s.SourceInvestigationsModel.LockObjectTx(ctx, tx, actor.TenantId, req.ObjectRef); err != nil {
			return types.SourceInvestigationData{}, err
		}
		row, lookupErr := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if errors.Is(lookupErr, model.ErrNotFound) {
			if req.ExpectedRevision != 0 {
				return types.SourceInvestigationData{}, investigationChanged()
			}
			row = &model.SourceInvestigations{Id: orderedID(), TenantId: actor.TenantId, ObjectRef: req.ObjectRef, StoreId: store, ObjectKey: key, Revision: 1, Status: req.Status, Note: req.Note, CreatedBy: actor.Id, UpdatedBy: actor.Id}
			err = s.SourceInvestigationsModel.InsertTx(ctx, tx, row)
		} else if lookupErr != nil {
			return types.SourceInvestigationData{}, lookupErr
		} else {
			if row.StoreId != store || row.ObjectKey != key {
				return types.SourceInvestigationData{}, errForbidden
			}
			if req.ExpectedRevision != row.Revision {
				return types.SourceInvestigationData{}, investigationChanged()
			}
			err = s.SourceInvestigationsModel.UpdateNoteTx(ctx, tx, row, req.Status, req.Note, actor.Id)
		}
		if err != nil {
			return types.SourceInvestigationData{}, err
		}
		saved, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return types.SourceInvestigationData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_investigate", ObjectType: "source_investigation", ObjectId: saved.Id, Source: "manual", Summary: fmt.Sprintf("Manual investigation revision %d, status %s; no storage or registration changes", saved.Revision, saved.Status)}); err != nil {
			return types.SourceInvestigationData{}, err
		}
		return investigationData(req.ObjectRef, saved), nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.SourceInvestigationData) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if result.ObjectRef != req.ObjectRef {
			return errForbidden
		}
		row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return err
		}
		return investigationResourceTx(ctx, s, tx, actor, row.ObjectKey)
	})
	if err != nil {
		return types.SourceInvestigationData{}, err
	}
	return result, nil
}
func investigationChanged() error {
	return failure(409, "INVESTIGATION_CHANGED", "调查记录已变化，请重新加载并核对说明")
}
