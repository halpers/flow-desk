package logic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const defaultInvestigationIdleDays int64 = 7
const minimumInvestigationIdleDays int64 = 1
const maximumInvestigationIdleDays int64 = 3650
const minimumInvestigationInterval int64 = 5
const maximumInvestigationInterval int64 = 10080
const defaultInvestigationInterval int64 = 60
const investigationAutomationTimeout = 5 * time.Second

func automationOwnerValid(row *model.InvestigationAutomationPolicies, actor *model.Users) bool {
	return actor.Enabled && actor.TenantId == row.TenantId && actor.Id == row.OwnerUserId && actor.AuthVersion == row.OwnerAuthVersion && actor.Role == "admin" && permitted(actor, "knowledge:manage") && permitted(actor, "conversation:use")
}
func automationDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.InvestigationAutomationPolicies, actor *model.Users) (types.InvestigationAutomationData, error) {
	result := types.InvestigationAutomationData{RuntimeEnabled: s.Config.InvestigationAutomationEnabled, Enabled: row.Enabled, Revision: row.Revision, IdleDays: row.IdleDays, IntervalMinutes: row.IntervalMinutes, OwnerUserId: row.OwnerUserId, NextRunAt: row.NextRunAt.UTC().Format(time.RFC3339Nano), LastOutcome: row.LastOutcome}
	if row.LastCheckedAt.Valid {
		result.LastCheckedAt = row.LastCheckedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	owner, err := s.UsersModel.FindOneTx(ctx, tx, row.OwnerUserId)
	if err != nil {
		return result, err
	}
	result.AuthorizationValid = automationOwnerValid(row, owner)
	if row.LastInvestigationId.Valid {
		investigation, err := s.SourceInvestigationsModel.FindOneByTenantIdIdTx(ctx, tx, actor.TenantId, row.LastInvestigationId.String)
		if err != nil {
			return result, err
		}
		if err = investigationResourceTx(ctx, s, tx, actor, investigation.ObjectKey); err == nil {
			result.LastObjectRef = investigation.ObjectRef
			result.LastRevision = row.LastRevision
		} else if !automationResourceHidden(err) {
			return result, err
		}
	}
	return result, nil
}
func GetInvestigationAutomation(ctx context.Context, s *svc.ServiceContext) (types.InvestigationAutomationData, error) {
	result := types.InvestigationAutomationData{RuntimeEnabled: s.Config.InvestigationAutomationEnabled, IdleDays: defaultInvestigationIdleDays, IntervalMinutes: defaultInvestigationInterval, LastOutcome: "never"}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		row, err := s.InvestigationAutomationPoliciesModel.FindOneForShareTx(ctx, tx, actor.TenantId)
		if errors.Is(err, model.ErrNotFound) {
			return validateCurrentIdentityTx(ctx, s, tx)
		}
		if err != nil {
			return err
		}
		result, err = automationDataTx(ctx, s, tx, row, actor)
		if err != nil {
			return err
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.InvestigationAutomationData{}, err
	}
	return result, nil
}
func UpdateInvestigationAutomation(ctx context.Context, s *svc.ServiceContext, req *types.UpdateInvestigationAutomationReq) (types.InvestigationAutomationData, error) {
	if !req.Confirm || req.ExpectedRevision < 0 || req.IdleDays < minimumInvestigationIdleDays || req.IdleDays > maximumInvestigationIdleDays || req.IntervalMinutes < minimumInvestigationInterval || req.IntervalMinutes > maximumInvestigationInterval {
		return types.InvestigationAutomationData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.investigation_automation", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.InvestigationAutomationData, error) {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return types.InvestigationAutomationData{}, err
		}
		if err = s.InvestigationAutomationPoliciesModel.InsertIfAbsentTx(ctx, tx, actor.TenantId, actor.Id, actor.AuthVersion); err != nil {
			return types.InvestigationAutomationData{}, err
		}
		row, err := s.InvestigationAutomationPoliciesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.InvestigationAutomationData{}, err
		}
		if row.Revision != req.ExpectedRevision {
			return types.InvestigationAutomationData{}, failure(409, "INVESTIGATION_AUTOMATION_CHANGED", "调查自动流转策略已变化，请刷新后重新确认")
		}
		row.Enabled = req.Enabled
		row.IdleDays = req.IdleDays
		row.IntervalMinutes = req.IntervalMinutes
		row.OwnerUserId = actor.Id
		row.OwnerAuthVersion = actor.AuthVersion
		if err = s.InvestigationAutomationPoliciesModel.UpdatePolicyTx(ctx, tx, row); err != nil {
			return types.InvestigationAutomationData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.investigation_automation", ObjectType: "investigation_automation_policy", ObjectId: row.Id, Source: "manual", Summary: fmt.Sprintf("Confirmed overdue return policy revision %d enabled=%t idle_days=%d interval_minutes=%d", row.Revision+1, row.Enabled, row.IdleDays, row.IntervalMinutes)}); err != nil {
			return types.InvestigationAutomationData{}, err
		}
		row, err = s.InvestigationAutomationPoliciesModel.FindOneForShareTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.InvestigationAutomationData{}, err
		}
		return automationDataTx(ctx, s, tx, row, actor)
	}, func(ctx context.Context, tx sqlx.Session, result *types.InvestigationAutomationData) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if result.LastObjectRef != "" {
			row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, result.LastObjectRef)
			if err != nil {
				return err
			}
			return investigationResourceTx(ctx, s, tx, actor, row.ObjectKey)
		}
		return nil
	})
}
func automationResourceHidden(err error) bool {
	return errors.Is(err, errForbidden) || errors.Is(err, errMissing) || errors.Is(err, model.ErrNotFound)
}

// The background grant is checked here only; no synthetic HTTP identity/session.
// Resource locks and the final current-owner lock follow manual writer order.
func automationResourceTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, key string) error {
	if !strings.HasPrefix(key, "knowledge/"+actor.TenantId+"/") {
		return errForbidden
	}
	hint := tenantInventoryVersionHint(actor.TenantId, key)
	if hint == "" {
		return nil
	}
	version, err := s.DocumentVersionsModel.FindInventoryIdentityTx(ctx, tx, actor.TenantId, hint)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := s.DocumentsModel.FindOneForShareTx(ctx, tx, actor.TenantId, version.DocumentId)
	if err != nil {
		return err
	}
	base, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, actor.TenantId, doc.KnowledgeBaseId)
	if err != nil {
		return err
	}
	if err = teamAccessTx(ctx, s, tx, actor, base.TeamId); err != nil {
		return err
	}
	if !readableBase(base, actor.Role) {
		return errMissing
	}
	return nil
}
func automationOwnerFenceTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.InvestigationAutomationPolicies) error {
	actor, err := s.UsersModel.FindOneForShareTx(ctx, tx, row.TenantId, row.OwnerUserId)
	if err != nil {
		return err
	}
	if !automationOwnerValid(row, actor) {
		return errForbidden
	}
	return nil
}
func DispatchInvestigationAutomation(ctx context.Context, s *svc.ServiceContext) (bool, error) {
	if !s.Config.InvestigationAutomationEnabled {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, investigationAutomationTimeout)
	defer cancel()
	ctx, span := otel.Tracer("flowdesk.scheduler").Start(ctx, "investigation.automation")
	defer span.End()
	found := false
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		policy, err := s.InvestigationAutomationPoliciesModel.FindDueForUpdateTx(ctx, tx)
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		actor, err := s.UsersModel.FindOneTx(ctx, tx, policy.OwnerUserId)
		if err != nil {
			return err
		}
		if !automationOwnerValid(policy, actor) {
			return s.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, policy, "authorization_required")
		}
		candidate, err := s.SourceInvestigationsModel.FindAutomationCandidateTx(ctx, tx, policy.TenantId, policy.CursorId.String, policy.IdleDays)
		if errors.Is(err, model.ErrNotFound) {
			policy.CursorId = sql.NullString{}
			if err = automationOwnerFenceTx(ctx, s, tx, policy); err != nil {
				return err
			}
			return s.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, policy, "empty")
		}
		if err != nil {
			return err
		}
		policy.CursorId = sql.NullString{String: candidate.Id, Valid: true}
		err = automationResourceTx(ctx, s, tx, actor, candidate.ObjectKey)
		if automationResourceHidden(err) {
			if err = automationOwnerFenceTx(ctx, s, tx, policy); err != nil {
				return err
			}
			return s.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, policy, "skipped")
		}
		if err != nil {
			return err
		}
		if err = s.SourceInvestigationsModel.LockObjectTx(ctx, tx, actor.TenantId, candidate.ObjectRef); err != nil {
			return err
		}
		current, err := s.SourceInvestigationsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, candidate.ObjectRef)
		if err != nil {
			return err
		}
		eligible, err := s.SourceInvestigationsModel.ExistsAutomationEligibleTx(ctx, tx, actor.TenantId, current.Id, candidate.Revision, policy.IdleDays)
		if err != nil {
			return err
		}
		if !eligible {
			if err = automationOwnerFenceTx(ctx, s, tx, policy); err != nil {
				return err
			}
			return s.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, policy, "skipped")
		}
		if err = s.SourceInvestigationsModel.UpdateNoteTx(ctx, tx, current, investigationOpen, current.Note, actor.Id); err != nil {
			return err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.investigation_auto_return", ObjectType: "source_investigation", ObjectId: current.Id, Source: "scheduled", Summary: fmt.Sprintf("Returned overdue investigating to open; policy revision %d, investigation revisions %d to %d; original note preserved", policy.Revision, current.Revision, current.Revision+1)}); err != nil {
			return err
		}
		if err = automationOwnerFenceTx(ctx, s, tx, policy); err != nil {
			return err
		}
		policy.LastInvestigationId = sql.NullString{String: current.Id, Valid: true}
		policy.LastRevision = current.Revision + 1
		span.SetAttributes(attribute.String("flowdesk.investigation_id", current.Id))
		return s.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, policy, "returned")
	})
	return found, err
}
