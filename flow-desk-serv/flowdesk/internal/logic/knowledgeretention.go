package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const versionRetained = "retained"
const versionCleanupPending = "cleanup_pending"
const versionPurged = "purged"

type cleanupCommand struct {
	TenantID    string `json:"tenant_id"`
	VersionID   string `json:"version_id"`
	JobID       string `json:"job_id"`
	ContentHash string `json:"content_hash"`
}

func requireRetained(row *model.DocumentVersions) error {
	if row.CleanupState != "" && row.CleanupState != versionRetained {
		return failure(409, "VERSION_RETIRED", "版本已进入清理流程，不能读取或重新使用")
	}
	return nil
}
func retentionItem(doc *model.Documents, row model.RetentionVersion, policy *model.KnowledgeBases, now time.Time) types.RetentionItem {
	reasons := []string{}
	if row.CleanupState != versionRetained {
		reasons = append(reasons, row.CleanupState)
	}
	if doc.ActiveVersionId.Valid && doc.ActiveVersionId.String == row.Id {
		reasons = append(reasons, "active")
	}
	if row.Referenced {
		reasons = append(reasons, "referenced")
	}
	if row.RecentRank <= policy.RetentionKeepLatest {
		reasons = append(reasons, "recent")
	}
	if now.Before(row.CreatedAt.Add(time.Duration(policy.RetentionMinimumAgeDays) * 24 * time.Hour)) {
		reasons = append(reasons, "too_young")
	}
	if row.HasArchive {
		reasons = append(reasons, "archive_history")
	}
	if row.Busy {
		reasons = append(reasons, "index_busy")
	}
	return types.RetentionItem{VersionId: row.Id, Filename: row.Filename, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), CleanupState: row.CleanupState, SourceSize: row.SourceSize, Eligible: len(reasons) == 0, Reasons: reasons}
}
func GetRetentionPlan(ctx context.Context, s *svc.ServiceContext, document string) (types.RetentionPlan, error) {
	result := types.RetentionPlan{DocumentId: document, Items: []types.RetentionItem{}}
	if !validID(document) {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		doc, err := sourceAdminDocumentTx(ctx, s, tx, document)
		if err != nil {
			return err
		}
		rows, err := s.DocumentVersionsModel.ListRetentionTx(ctx, tx, doc.TenantId, doc.Id)
		if err != nil {
			return err
		}
		result.DocumentVersion = doc.Version
		policy, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, doc.TenantId, doc.KnowledgeBaseId)
		if err != nil {
			return err
		}
		result.KeepLatest = policy.RetentionKeepLatest
		result.MinimumAgeDays = policy.RetentionMinimumAgeDays
		result.PolicyRevision = policy.RetentionRevision
		now := time.Now()
		for _, row := range rows {
			result.Items = append(result.Items, retentionItem(doc, row, policy, now))
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	return result, err
}
func CleanupDocumentVersion(ctx context.Context, s *svc.ServiceContext, req *types.CleanupVersionReq) (types.VersionData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.VersionData{}, err
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || !req.Confirm || req.ExpectedDocumentVersion < 1 || req.ExpectedPolicyRevision < 0 {
		return types.VersionData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.cleanup", req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.VersionData, error) {
		// Document exclusion serializes activation, uploads and cleanup.
		doc, err := s.DocumentsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.DocumentId)
		if err != nil {
			return types.VersionData{}, err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return types.VersionData{}, err
		}
		if doc.Version != req.ExpectedDocumentVersion {
			return types.VersionData{}, failure(409, "VERSION_CONFLICT", "文档状态已变化，请重新预览")
		}
		policy, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, actor.TenantId, doc.KnowledgeBaseId)
		if err != nil {
			return types.VersionData{}, err
		}
		if (req.ExpectedPolicyRevision == 0 && policy.RetentionRevision != 1) || (req.ExpectedPolicyRevision != 0 && policy.RetentionRevision != req.ExpectedPolicyRevision) {
			return types.VersionData{}, policyChanged()
		}
		row, err := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.VersionId)
		if err != nil {
			return types.VersionData{}, err
		}
		if row.DocumentId != doc.Id {
			return types.VersionData{}, errMissing
		}
		rows, err := s.DocumentVersionsModel.ListRetentionTx(ctx, tx, actor.TenantId, doc.Id)
		if err != nil {
			return types.VersionData{}, err
		}
		eligible := false
		for _, candidate := range rows {
			if candidate.Id == row.Id {
				eligible = retentionItem(doc, candidate, policy, time.Now()).Eligible
			}
		}
		if !eligible {
			return types.VersionData{}, failure(409, "VERSION_PROTECTED", "版本仍受保留规则保护，请刷新清理预览")
		}
		if err = s.DocumentVersionsModel.UpdateCleanupStateTx(ctx, tx, actor.TenantId, row.Id, versionRetained, versionCleanupPending); err != nil {
			return types.VersionData{}, err
		}
		row.CleanupState = versionCleanupPending
		payload := marshal(cleanupCommand{actor.TenantId, row.Id, row.JobId, row.ContentHash})
		if err = s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: orderedID(), TenantId: actor.TenantId, AggregateId: doc.Id, DispatchId: uuid.NewSHA1(uuid.MustParse(row.Id), []byte("knowledge.cleanup")).String(), Topic: "knowledge.cleanup", Payload: payload, Status: "pending"}); err != nil {
			return types.VersionData{}, err
		}
		if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.cleanup", ObjectType: "document_version", ObjectId: row.Id, Source: "manual", Summary: fmt.Sprintf("Version retirement confirmed under policy revision %d (latest %d, age days %d); index and database content cleanup queued", policy.RetentionRevision, policy.RetentionKeepLatest, policy.RetentionMinimumAgeDays)}); err != nil {
			return types.VersionData{}, err
		}
		return versionData(row), nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.VersionData) error {
		row, err := sourceAdminVersionTx(ctx, s, tx, result.DocumentId, result.Id)
		if err == nil {
			*result = versionData(row)
		}
		return err
	})
}
func CleanupVersionIndex(ctx context.Context, s *svc.ServiceContext, event *model.OutboxEvents) error {
	var command cleanupCommand
	if json.Unmarshal([]byte(event.Payload), &command) != nil || command.TenantID != event.TenantId || !validID(command.VersionID) || !validID(command.JobID) {
		return errArgument
	}
	row, err := s.DocumentVersionsModel.FindOne(ctx, command.VersionID)
	if err != nil {
		return err
	}
	if row.TenantId != event.TenantId || row.DocumentId != event.AggregateId || row.JobId != command.JobID || row.ContentHash != command.ContentHash {
		return errForbidden
	}
	if row.CleanupState == versionPurged {
		return nil
	}
	if row.CleanupState != versionCleanupPending {
		return errArgument
	}
	if err = s.AgentClient.CleanupKnowledge(ctx, event.Payload); err != nil {
		return err
	}
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		current, err := s.DocumentVersionsModel.FindOneByJobIdForUpdateTx(ctx, tx, command.JobID)
		if err != nil {
			return err
		}
		if _, err = s.OutboxEventsModel.FindOneOwnedForUpdateTx(ctx, tx, event.Id, event.AttemptCount); err != nil {
			return err
		}
		if current.CleanupState == versionPurged {
			return nil
		}
		return s.DocumentVersionsModel.UpdateCleanupStateTx(ctx, tx, event.TenantId, current.Id, versionCleanupPending, versionPurged)
	})
}
