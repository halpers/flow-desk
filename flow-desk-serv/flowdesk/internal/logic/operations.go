package logic

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maxRunRetries int64 = 3
const maxOperationsPage int64 = 50

// Reload the viewer after slow external calls as roles, sessions and teams may have changed.
func operationsViewerTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session) (*model.Users, error) {
	current, err := identityRequired(ctx)
	if err != nil {
		return nil, err
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, current.User.Id)
	if err != nil {
		return nil, err
	}
	if !actor.Enabled || actor.TenantId != current.User.TenantId || !permitted(actor, "conversation:use") {
		return nil, errForbidden
	}
	live, err := s.SessionsModel.ExistsLiveFamilyBySessionTx(ctx, tx, actor.TenantId, actor.Id, current.Session.Id)
	if err != nil {
		return nil, err
	}
	if !live {
		return nil, ErrUnauthorized
	}
	return actor, nil
}

func operationDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, row *model.Runs) (types.RunOperationData, error) {
	data := types.RunOperationData{Id: row.Id, ConversationId: row.ConversationId, ActorUserId: row.ActorUserId, Status: row.Status, Version: row.Version, ErrorCode: row.ErrorCode, ToolCallCount: row.ToolCallCount, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano), RetryOfRunId: row.RetryOfRunId.String, RetryDepth: row.RetryDepth}
	child, err := s.RunsModel.FindOneRetryTx(ctx, tx, row.TenantId, row.Id)
	if err == nil {
		data.RetriedByRunId = child.Id
	} else if !errors.Is(err, model.ErrNotFound) {
		return data, err
	}
	hasActions, err := s.ActionsModel.ExistsByRunTx(ctx, tx, row.TenantId, row.Id)
	if err != nil {
		return data, err
	}
	switch {
	case actor.Id != row.ActorUserId:
		data.RetryBlockReason = "OWNER_REQUIRED"
	case row.Status != domain.RunFailed && row.Status != domain.RunExpired:
		data.RetryBlockReason = "NOT_RETRYABLE_STATE"
	case hasActions || row.InputCount > 0:
		data.RetryBlockReason = "RETRY_REQUIRES_REVIEW"
	case data.RetriedByRunId != "":
		data.RetryBlockReason = "ALREADY_RETRIED"
	case row.RetryDepth >= maxRunRetries:
		data.RetryBlockReason = "RETRY_LIMIT"
	default:
		data.CanRetry = true
	}
	return data, nil
}

func ListRuns(ctx context.Context, s *svc.ServiceContext, req *types.RunsPageReq) (types.RunOperationPage, error) {
	result := types.RunOperationPage{Items: []types.RunOperationData{}}
	if req.Scope == "" {
		req.Scope = "own"
	}
	if req.Limit == 0 {
		req.Limit = 20
	}
	if (req.Scope != "own" && req.Scope != "tenant") || req.Limit < 1 || req.Limit > maxOperationsPage || (req.RunId != "" && !validID(req.RunId)) {
		return result, errArgument
	}
	if req.Status != "" && !slices.Contains([]string{"queued", "running", "waiting_input", "waiting_approval", "cancelling", "succeeded", "failed", "cancelled", "expired"}, req.Status) {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := operationsViewerTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if req.Scope == "tenant" && !permitted(actor, "run:observe") {
			return errForbidden
		}
		scope := strings.Join([]string{"runs", actor.TenantId, actor.Id, req.Scope, req.Status, req.RunId}, ":")
		position, err := decodeCursor(req.Cursor, scope)
		if err != nil {
			return err
		}
		filter := model.RunOperationFilter{Tenant: actor.TenantId, Actor: actor.Id, Role: actor.Role, Scope: req.Scope, Status: req.Status, RunID: req.RunId, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), BeforeID: "ffffffff-ffff-ffff-ffff-ffffffffffff", Limit: req.Limit + 1}
		if position != "" {
			parts := strings.Split(position, "|")
			if len(parts) != 2 || !validID(parts[1]) {
				return errArgument
			}
			filter.Before, err = time.Parse(time.RFC3339Nano, parts[0])
			if err != nil {
				return errArgument
			}
			filter.BeforeID = parts[1]
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return err
		}
		rows, err := s.RunsModel.ListOperationsTx(ctx, tx, filter)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > req.Limit
		if result.HasMore {
			rows = rows[:req.Limit]
		}
		for i := range rows {
			item, err := operationDataTx(ctx, s, tx, actor, &rows[i])
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if result.HasMore {
			last := rows[len(rows)-1]
			result.NextCursor = encodeCursor(scope, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"|"+last.Id)
		}
		return nil
	})
	return result, err
}

func visibleRunOperation(ctx context.Context, s *svc.ServiceContext, id string) (types.RunOperationData, error) {
	var data types.RunOperationData
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := operationsViewerTx(ctx, s, tx)
		if err != nil {
			return err
		}
		// Lock the run before authorization locks, matching event writers.
		if _, err = s.RunsModel.FindOneForShareTx(ctx, tx, actor.TenantId, id); err != nil {
			return err
		}
		scope := "own"
		if permitted(actor, "run:observe") {
			scope = "tenant"
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return err
		}
		row, err := s.RunsModel.FindOneVisibleForShareTx(ctx, tx, model.RunOperationFilter{Tenant: actor.TenantId, Actor: actor.Id, Role: actor.Role, Scope: scope, RunID: id})
		if err != nil {
			return err
		}
		data, err = operationDataTx(ctx, s, tx, actor, row)
		return err
	})
	return data, err
}
func GetRunTrace(ctx context.Context, s *svc.ServiceContext, id string) (types.RunTraceData, error) {
	var data types.RunTraceData
	if !validID(id) {
		return data, errArgument
	}
	if _, err := visibleRunOperation(ctx, s, id); err != nil {
		return data, err
	}
	observation, callErr := s.AgentClient.Observation(ctx, Current(ctx).User.TenantId, id)
	run, err := visibleRunOperation(ctx, s, id)
	if err != nil {
		return data, err
	}
	if callErr != nil {
		return data, failure(503, "OBSERVATION_UNAVAILABLE", "执行记录暂不可用，请稍后刷新")
	}
	return types.RunTraceData{Run: run, Observation: observation}, nil
}

func RetryRun(ctx context.Context, s *svc.ServiceContext, req *types.RetryRunReq) (types.RunData, error) {
	if !validID(req.RunId) || req.ExpectedVersion < 1 {
		return types.RunData{}, errArgument
	}
	// This also rechecks source access on an idempotency replay, before returning a child.
	visible, err := visibleRunOperation(ctx, s, req.RunId)
	if err != nil {
		return types.RunData{}, err
	}
	if visible.ActorUserId != Current(ctx).User.Id {
		return types.RunData{}, errMissing
	}
	return idempotentAuthorized(ctx, s, "run.retry:"+req.RunId, req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.RunData, error) {
		actor, err := operationsViewerTx(ctx, s, tx)
		if err != nil {
			return types.RunData{}, err
		}
		conversation, err := s.ConversationsModel.FindOneOwnedForUpdateTx(ctx, tx, actor.TenantId, actor.Id, visible.ConversationId)
		if err != nil {
			return types.RunData{}, err
		}
		parent, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.RunId)
		if err != nil {
			return types.RunData{}, err
		}
		if parent.ActorUserId != actor.Id {
			return types.RunData{}, errMissing
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return types.RunData{}, err
		}
		denied, err := s.RunTicketResourcesModel.ExistsInaccessibleTx(ctx, tx, actor.TenantId, parent.Id, actor.Id, actor.Role)
		if err != nil {
			return types.RunData{}, err
		}
		if denied {
			return types.RunData{}, errMissing
		}
		child, err := s.RunsModel.FindOneRetryTx(ctx, tx, actor.TenantId, parent.Id)
		if err == nil {
			return runDataTx(ctx, s, tx, child)
		}
		if !errors.Is(err, model.ErrNotFound) {
			return types.RunData{}, err
		}
		if parent.Version != req.ExpectedVersion {
			return types.RunData{}, failure(409, "VERSION_CONFLICT", "任务状态已变化，请刷新")
		}
		metadata, err := operationDataTx(ctx, s, tx, actor, parent)
		if err != nil {
			return types.RunData{}, err
		}
		if !metadata.CanRetry {
			return types.RunData{}, failure(409, metadata.RetryBlockReason, "此任务不能直接重试，请查看原会话及办理回执")
		}
		input, err := s.MessagesModel.FindOneTx(ctx, tx, actor.TenantId, parent.InputMessageId)
		if err != nil {
			return types.RunData{}, err
		}
		result, err := enqueueRunTx(ctx, s, tx, conversation, input.Content, uuid.NewString(), parent)
		if err != nil {
			return types.RunData{}, err
		}
		err = recordTeamAuditTx(ctx, s, tx, actor, "run.retry", "run", result.Id, marshal(map[string]string{"retry_of_run_id": parent.Id}))
		return result, err
	}, func(ctx context.Context, tx sqlx.Session, result *types.RunData) error {
		actor, err := operationsViewerTx(ctx, s, tx)
		if err != nil {
			return err
		}
		parent, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, actor.TenantId, actor.Id, req.RunId)
		if err != nil {
			return err
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return err
		}
		denied, err := s.RunTicketResourcesModel.ExistsInaccessibleTx(ctx, tx, actor.TenantId, parent.Id, actor.Id, actor.Role)
		if err != nil {
			return err
		}
		if denied {
			return errMissing
		}
		child, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, actor.TenantId, actor.Id, result.Id)
		if err != nil {
			return err
		}
		*result, err = runDataTx(ctx, s, tx, child)
		return err
	})
}
