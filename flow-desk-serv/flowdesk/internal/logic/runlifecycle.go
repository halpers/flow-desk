package logic

import (
	"context"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const lifecycleMinimumAgeDays int64 = 30
const lifecycleInventoryTimeout = 3 * time.Second

// Each service is observed separately. A candidate is planning metadata, never a cleanup permit.
func GetRunLifecycle(ctx context.Context, s *svc.ServiceContext, id string) (types.RunLifecycleData, error) {
	var result types.RunLifecycleData
	if !validID(id) {
		return result, errArgument
	}
	before, err := visibleRunOperation(ctx, s, id)
	if err != nil {
		return result, err
	}
	agent, callErr := s.AgentClient.Lifecycle(ctx, Current(ctx).User.TenantId, id)
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := operationsViewerTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if _, err = s.RunsModel.FindOneForShareTx(ctx, tx, actor.TenantId, id); err != nil {
			return err
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return err
		}
		scope := "own"
		if permitted(actor, "run:observe") {
			scope = "tenant"
		}
		row, err := s.RunsModel.FindOneVisibleForShareTx(ctx, tx, model.RunOperationFilter{Tenant: actor.TenantId, Actor: actor.Id, Role: actor.Role, Scope: scope, RunID: id})
		if err != nil {
			return err
		}
		if callErr != nil {
			return failure(503, "LIFECYCLE_UNAVAILABLE", "生命周期盘点暂不可用，请稍后刷新")
		}
		if row.Version != before.Version {
			return failure(409, "LIFECYCLE_CHANGED", "任务状态已变化，请重新盘点")
		}
		queryCtx, cancel := context.WithTimeout(ctx, lifecycleInventoryTimeout)
		defer cancel()
		inventory, err := s.RunsModel.FindLifecycleInventoryTx(queryCtx, tx, actor.TenantId, id)
		if err != nil {
			return err
		}
		run, err := operationDataTx(ctx, s, tx, actor, row)
		if err != nil {
			return err
		}
		result = buildRunLifecycle(run, inventory, agent)
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return types.RunLifecycleData{}, err
	}
	return result, nil
}

func buildRunLifecycle(run types.RunOperationData, inventory model.RunLifecycleInventory, agent types.AgentLifecycleData) types.RunLifecycleData {
	result := types.RunLifecycleData{Run: run, ObservedAt: inventory.ObservedAt.UTC().Format(time.RFC3339Nano), MinimumAgeDays: lifecycleMinimumAgeDays, ObservationOnly: true, ProtectionReasons: []string{}, Agent: agent}
	result.Business = types.BusinessLifecycleData{
		MessageCount:               inventory.MessageCount,
		EventCount:                 inventory.EventCount,
		ActionCount:                inventory.ActionCount,
		ApprovalCount:              inventory.ApprovalCount,
		PendingApprovalCount:       inventory.PendingApprovalCount,
		PendingInputCount:          inventory.PendingInputCount,
		ReceiptCount:               inventory.ReceiptCount,
		EvidenceCount:              inventory.EvidenceCount,
		DispatchCount:              inventory.DispatchCount,
		UnsettledDispatchCount:     inventory.UnsettledDispatchCount,
		RetryLinkCount:             inventory.RetryLinkCount,
		ActiveConversationRunCount: inventory.ActiveConversationRunCount,
		ResourceReferenceCount:     inventory.ResourceReferenceCount,
		LeaseActive:                inventory.LeaseActive,
	}
	protect := func(condition bool, reason string) {
		if condition {
			result.ProtectionReasons = append(result.ProtectionReasons, reason)
		}
	}
	protect(!domain.IsTerminal(run.Status), "RUN_NOT_TERMINAL")
	protect(!inventory.OldEnough, "MINIMUM_AGE_NOT_REACHED")
	protect(inventory.LeaseActive, "BUSINESS_LEASE_ACTIVE")
	protect(inventory.ActiveConversationRunCount > 0, "CONVERSATION_ACTIVE")
	protect(inventory.UnsettledDispatchCount > 0, "DISPATCH_UNSETTLED")
	protect(inventory.PendingApprovalCount > 0, "APPROVAL_UNSETTLED")
	protect(inventory.PendingInputCount > 0, "INPUT_UNSETTLED")
	protect(inventory.ActionCount > 0 || inventory.ReceiptCount > 0 || inventory.ResourceReferenceCount > 0, "BUSINESS_REFERENCES")
	protect(inventory.EvidenceCount > 0, "EVIDENCE_REFERENCES")
	protect(inventory.RetryLinkCount > 0, "RETRY_CHAIN_REFERENCES")
	result.ProtectionReasons = append(result.ProtectionReasons, agent.ProtectionReasons...)
	result.ArchiveCandidate = agent.Available && len(result.ProtectionReasons) == 0
	return result
}
