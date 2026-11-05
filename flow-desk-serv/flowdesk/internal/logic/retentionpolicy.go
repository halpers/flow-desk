package logic

import (
	"context"
	"fmt"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const minimumRetentionVersions int64 = 1
const maximumRetentionVersions int64 = maxVersionsPerDocument
const minimumRetentionDays int64 = 1
const maximumRetentionDays int64 = 3650

func retentionPolicyData(row *model.KnowledgeBases) types.RetentionPolicyData {
	return types.RetentionPolicyData{KnowledgeBaseId: row.Id, KeepLatest: row.RetentionKeepLatest, MinimumAgeDays: row.RetentionMinimumAgeDays, Revision: row.RetentionRevision}
}
func policyChanged() error {
	return failure(409, "RETENTION_POLICY_CHANGED", "保留策略已变化，请刷新预览并重新确认")
}
func GetRetentionPolicy(ctx context.Context, s *svc.ServiceContext, baseID string) (types.RetentionPolicyData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.RetentionPolicyData{}, err
	}
	if !validID(baseID) {
		return types.RetentionPolicyData{}, errArgument
	}
	var result types.RetentionPolicyData
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, actor.TenantId, baseID)
		if err != nil {
			return err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, baseID); err != nil {
			return err
		}
		result = retentionPolicyData(row)
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	return result, err
}
func UpdateRetentionPolicy(ctx context.Context, s *svc.ServiceContext, req *types.UpdateRetentionPolicyReq) (types.RetentionPolicyData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.RetentionPolicyData{}, err
	}
	if !validID(req.KnowledgeBaseId) || req.ExpectedRevision < 1 || req.KeepLatest < minimumRetentionVersions || req.KeepLatest > maximumRetentionVersions || req.MinimumAgeDays < minimumRetentionDays || req.MinimumAgeDays > maximumRetentionDays {
		return types.RetentionPolicyData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.retention_policy", req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RetentionPolicyData, error) {
		row, err := s.KnowledgeBasesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.KnowledgeBaseId)
		if err != nil {
			return types.RetentionPolicyData{}, err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, row.Id); err != nil {
			return types.RetentionPolicyData{}, err
		}
		if row.RetentionRevision != req.ExpectedRevision {
			return types.RetentionPolicyData{}, policyChanged()
		}
		if err = s.KnowledgeBasesModel.UpdateRetentionPolicyTx(ctx, tx, row, req.KeepLatest, req.MinimumAgeDays); err != nil {
			return types.RetentionPolicyData{}, err
		}
		summary := fmt.Sprintf("Retention policy revision %d to %d: latest %d to %d, age days %d to %d; manual cleanup only", row.RetentionRevision, row.RetentionRevision+1, row.RetentionKeepLatest, req.KeepLatest, row.RetentionMinimumAgeDays, req.MinimumAgeDays)
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.retention_policy", ObjectType: "knowledge_base", ObjectId: row.Id, Source: "manual", Summary: summary}); err != nil {
			return types.RetentionPolicyData{}, err
		}
		row.RetentionKeepLatest = req.KeepLatest
		row.RetentionMinimumAgeDays = req.MinimumAgeDays
		row.RetentionRevision++
		return retentionPolicyData(row), nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.RetentionPolicyData) error {
		if result.KnowledgeBaseId != req.KnowledgeBaseId {
			return errMissing
		}
		return knowledgeManagementBaseTx(ctx, s, tx, actor, result.KnowledgeBaseId)
	})
}
