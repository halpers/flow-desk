package logic

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func registerWaitTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, kind string, payload map[string]any) error {
	if run.Status != domain.RunRunning {
		return errStale
	}
	expires := time.Now().UTC().Add(waitLifetime)
	if kind == domain.EventRunWaitingApproval {
		actionID, ok := payload["action_id"].(string)
		if !ok || !validID(actionID) {
			return errArgument
		}
		action, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, actionID)
		if err != nil {
			return err
		}
		if action.RunId != run.Id || action.Status != statePrepared {
			return errStale
		}
		approval, err := s.ApprovalsModel.FindOneByActionForUpdateTx(ctx, tx, run.TenantId, actionID)
		if err != nil {
			return err
		}
		if approval.Status != statePrepared || time.Now().After(approval.ExpiresAt) {
			return errStale
		}
		approval.Status = statePending
		if err = s.ApprovalsModel.UpdateDecisionTx(ctx, tx, approval); err != nil {
			return err
		}
		expires = approval.ExpiresAt
		run.Status = domain.RunWaitingApproval
		payload["approval_id"] = approval.Id
	} else {
		id, ok := payload["input_id"].(string)
		if !ok || !validID(id) {
			return errArgument
		}
		question, ok := payload["question"].(string)
		if !ok || strings.TrimSpace(question) == "" || utf8.RuneCountInString(question) > 1000 {
			return errArgument
		}
		if run.InputCount >= 3 {
			return failure(429, "INPUT_LIMIT", "补充信息次数已达到上限")
		}
		if err := s.PendingInputsModel.InsertTx(ctx, tx, &model.PendingInputs{Id: id, TenantId: run.TenantId, RunId: run.Id, Question: question, Status: statePending, Version: 1, ExpiresAt: expires}); err != nil {
			return err
		}
		run.Status = domain.RunWaitingInput
	}
	run.WaitExpiresAt = sql.NullTime{Time: expires, Valid: true}
	run.LeaseExpiresAt = sql.NullTime{}
	if err := s.RunsModel.UpdateWaitingTx(ctx, tx, run); err != nil {
		return err
	}
	payload["status"] = run.Status
	payload["run_version"] = run.Version + 1
	return nil
}
func resumeRunTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, data map[string]any) error {
	run.Status = domain.RunQueued
	run.DispatchId = uuid.NewString()
	run.LeaseGeneration++
	run.DeadlineAt = time.Now().UTC().Add(runDeadline)
	run.WaitExpiresAt = sql.NullTime{}
	run.ResumeData = marshal(data)
	if err := s.RunsModel.UpdateResumeTx(ctx, tx, run); err != nil {
		return err
	}
	if _, err := appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.MustParse(run.DispatchId), []byte("queued")).String(), domain.EventRunQueued, map[string]any{"status": run.Status, "run_version": run.Version + 1}, time.Now().UTC(), ""); err != nil {
		return err
	}
	command := map[string]any{"dispatch_id": run.DispatchId, "run_id": run.Id, "lease_generation": run.LeaseGeneration, "command": "resume", "graph_version": "ticket-v1"}
	return s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: uuid.NewString(), TenantId: run.TenantId, AggregateId: run.Id, DispatchId: run.DispatchId, Topic: "run.resume", Payload: marshal(command), Status: statePending, AvailableAt: time.Now().UTC()})
}
func invalidatePendingTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, status string) error {
	actions, err := s.ActionsModel.ListByRunTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return err
	}
	for _, item := range actions {
		if item.Status != statePrepared {
			continue
		}
		action, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, item.Id)
		if err != nil {
			return err
		}
		if err = s.ActionsModel.UpdateStatusTx(ctx, tx, run.TenantId, action.Id, stateCancelled); err != nil {
			return err
		}
		approval, err := s.ApprovalsModel.FindOneByActionForUpdateTx(ctx, tx, run.TenantId, action.Id)
		if err != nil {
			return err
		}
		if approval.Status == statePrepared || approval.Status == statePending || approval.Status == stateApproved {
			approval.Status = status
			if err = s.ApprovalsModel.UpdateDecisionTx(ctx, tx, approval); err != nil {
				return err
			}
		}
	}
	inputs, err := s.PendingInputsModel.ListPendingByRunTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		input.Status = stateCancelled
		if status == stateExpired {
			input.Status = stateExpired
		}
		if err = s.PendingInputsModel.UpdateStatusTx(ctx, tx, &input); err != nil {
			return err
		}
	}
	return nil
}
func terminateRunTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, code string) error {
	if err := invalidatePendingTx(ctx, s, tx, run, stateCancelled); err != nil {
		return err
	}
	run.Status = domain.RunCancelled
	run.ErrorCode = code
	run.LeaseGeneration++
	if err := s.RunsModel.UpdateResumeTx(ctx, tx, run); err != nil {
		return err
	}
	if err := s.MessagesModel.UpdateAbortedTx(ctx, tx, run.ResultMessageId); err != nil {
		return err
	}
	if _, err := appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.MustParse(run.Id), []byte(stateCancelled)).String(), domain.EventRunCancelled, map[string]any{"status": run.Status, "run_version": run.Version + 1, "error_code": code}, time.Now().UTC(), ""); err != nil {
		return err
	}
	dispatch := uuid.NewString()
	command := map[string]any{"dispatch_id": dispatch, "run_id": run.Id, "lease_generation": run.LeaseGeneration}
	return s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: uuid.NewString(), TenantId: run.TenantId, AggregateId: run.Id, DispatchId: dispatch, Topic: "run.cancel", Payload: marshal(command), Status: statePending, AvailableAt: time.Now().UTC()})
}
func CancelRun(ctx context.Context, s *svc.ServiceContext, req *types.CancelRunReq) (types.RunData, error) {
	if !validID(req.RunId) || req.ExpectedVersion < 1 {
		return types.RunData{}, errArgument
	}
	return idempotent(ctx, s, "run.cancel:"+req.RunId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RunData, error) {
		actor := Current(ctx).User
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.RunId)
		if err != nil {
			return types.RunData{}, err
		}
		if run.ActorUserId != actor.Id {
			return types.RunData{}, errMissing
		}
		if domain.IsTerminal(run.Status) {
			return runDataTx(ctx, s, tx, run)
		}
		if run.Version != req.ExpectedVersion {
			return types.RunData{}, failure(409, "VERSION_CONFLICT", "任务进度已变化，请刷新后取消")
		}
		if err = terminateRunTx(ctx, s, tx, run, "USER_CANCELLED"); err != nil {
			return types.RunData{}, err
		}
		return runDataTx(ctx, s, tx, run)
	})
}
func DecideApproval(ctx context.Context, s *svc.ServiceContext, req *types.DecisionReq) (types.RunData, error) {
	if !validID(req.ApprovalId) || req.ExpectedVersion < 1 || (req.Decision != "approve" && req.Decision != "reject") || utf8.RuneCountInString(req.Reason) > 500 {
		return types.RunData{}, errArgument
	}
	current, err := identityRequired(ctx)
	if err != nil {
		return types.RunData{}, err
	}
	locator, err := s.ApprovalsModel.FindOneByTenantIdId(ctx, current.User.TenantId, req.ApprovalId)
	if err != nil {
		return types.RunData{}, err
	}
	return idempotent(ctx, s, "approval.decide:"+req.ApprovalId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.RunData, error) {
		actor := Current(ctx).User
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, locator.RunId)
		if err != nil {
			return types.RunData{}, err
		}
		if run.ActorUserId != actor.Id {
			return types.RunData{}, errMissing
		}
		action, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, locator.ActionId)
		if err != nil {
			return types.RunData{}, err
		}
		approval, err := s.ApprovalsModel.FindOneByActionForUpdateTx(ctx, tx, run.TenantId, action.Id)
		if err != nil {
			return types.RunData{}, err
		}
		if approval.Status != statePending || run.Status != domain.RunWaitingApproval || approval.Version != req.ExpectedVersion {
			return types.RunData{}, failure(409, "VERSION_CONFLICT", "确认状态已变化，请刷新")
		}
		if time.Now().After(approval.ExpiresAt) {
			return types.RunData{}, failure(410, "APPROVAL_EXPIRED", "确认已过期")
		}
		fresh, err := s.UsersModel.FindOneTx(ctx, tx, actor.Id)
		if err != nil {
			return types.RunData{}, err
		}
		if !fresh.Enabled || !permitted(fresh, actionPermission(action.ToolName)) {
			return types.RunData{}, errForbidden
		}
		approval.Status = stateApproved
		if req.Decision == "reject" {
			approval.Status = stateRejected
		}
		approval.DecidedBy = sql.NullString{String: actor.Id, Valid: true}
		approval.DecisionReason = req.Reason
		approval.DecidedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
		if err = s.ApprovalsModel.UpdateDecisionTx(ctx, tx, approval); err != nil {
			return types.RunData{}, err
		}
		if _, err = appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.MustParse(approval.Id), []byte("decision")).String(), domain.EventApprovalDecided, map[string]any{"approval_id": approval.Id, "status": approval.Status}, time.Now().UTC(), ""); err != nil {
			return types.RunData{}, err
		}
		if req.Decision == "reject" {
			err = terminateRunTx(ctx, s, tx, run, "APPROVAL_REJECTED")
		} else {
			if err = authorizeRunTx(ctx, s, tx, run); err != nil {
				return types.RunData{}, err
			}
			err = resumeRunTx(ctx, s, tx, run, map[string]any{"decision": stateApproved, "action_id": action.Id})
		}
		if err != nil {
			return types.RunData{}, err
		}
		return runDataTx(ctx, s, tx, run)
	})
}
func SubmitInput(ctx context.Context, s *svc.ServiceContext, req *types.SubmitInputReq) (types.RunData, error) {
	if !validID(req.RunId) || !validID(req.InputId) || req.ExpectedVersion < 1 || strings.TrimSpace(req.Content) == "" || utf8.RuneCountInString(req.Content) > 2000 {
		return types.RunData{}, errArgument
	}
	current, err := identityRequired(ctx)
	if err != nil {
		return types.RunData{}, err
	}
	locator, err := s.RunsModel.FindOneByTenantIdId(ctx, current.User.TenantId, req.RunId)
	if err != nil {
		return types.RunData{}, err
	}
	return idempotent(ctx, s, "run.input:"+req.RunId, req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.RunData, error) {
		actor := Current(ctx).User
		conversation, err := s.ConversationsModel.FindOneOwnedForUpdateTx(ctx, tx, actor.TenantId, actor.Id, locator.ConversationId)
		if err != nil {
			return types.RunData{}, err
		}
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.RunId)
		if err != nil {
			return types.RunData{}, err
		}
		if run.ActorUserId != actor.Id {
			return types.RunData{}, errMissing
		}
		if run.Status != domain.RunWaitingInput || run.Version != req.ExpectedVersion {
			return types.RunData{}, failure(409, "VERSION_CONFLICT", "补充请求已变化，请刷新")
		}
		input, err := s.PendingInputsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.InputId)
		if err != nil {
			return types.RunData{}, err
		}
		if input.RunId != run.Id || input.Status != statePending {
			return types.RunData{}, errMissing
		}
		if time.Now().After(input.ExpiresAt) {
			return types.RunData{}, failure(410, "INPUT_EXPIRED", "补充信息请求已过期")
		}
		if err = authorizeRunTx(ctx, s, tx, run); err != nil {
			return types.RunData{}, err
		}
		input.Status = stateAnswered
		input.Answer = req.Content
		if err = s.PendingInputsModel.UpdateStatusTx(ctx, tx, input); err != nil {
			return types.RunData{}, err
		}
		assistant, err := s.MessagesModel.FindOneTx(ctx, tx, actor.TenantId, run.ResultMessageId)
		if err != nil {
			return types.RunData{}, err
		}
		if err = s.MessagesModel.UpdateInputPositionTx(ctx, tx, assistant.Id, assistant.Position, conversation.MessageCount+1); err != nil {
			return types.RunData{}, err
		}
		message := &model.Messages{Id: uuid.NewString(), TenantId: actor.TenantId, ConversationId: conversation.Id, RunId: run.Id, Role: "user", Position: assistant.Position, ClientMessageId: sql.NullString{String: req.InputId, Valid: true}, Content: req.Content, Status: "final", Generation: 1, Citations: "[]", ActionReceipts: "[]"}
		if err = s.MessagesModel.InsertTx(ctx, tx, message); err != nil {
			return types.RunData{}, err
		}
		if err = s.ConversationsModel.UpdateInputCountTx(ctx, tx, conversation.Id, conversation.MessageCount); err != nil {
			return types.RunData{}, err
		}
		run.InputCount++
		if err = resumeRunTx(ctx, s, tx, run, map[string]any{"input": req.Content, "input_id": input.Id}); err != nil {
			return types.RunData{}, err
		}
		return runDataTx(ctx, s, tx, run)
	})
}

func addRunControlsTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.Runs, result *types.RunData) error {
	result.PendingInputs = []types.PendingInputData{}
	result.Approvals = []types.ApprovalData{}
	result.ActionReceipts = []types.ReceiptData{}
	inputs, err := s.PendingInputsModel.ListPendingByRunTx(ctx, tx, row.TenantId, row.Id)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		result.PendingInputs = append(result.PendingInputs, types.PendingInputData{Id: input.Id, Question: input.Question, Status: input.Status, Version: input.Version, ExpiresAt: input.ExpiresAt.UTC().Format(time.RFC3339Nano)})
	}
	approvals, err := s.ApprovalsModel.ListByRunTx(ctx, tx, row.TenantId, row.Id)
	if err != nil {
		return err
	}
	for i := range approvals {
		result.Approvals = append(result.Approvals, approvalData(&approvals[i]))
	}
	receipts, err := s.ActionReceiptsModel.ListByRunTx(ctx, tx, row.TenantId, row.Id)
	if err != nil {
		return err
	}
	for i := range receipts {
		result.ActionReceipts = append(result.ActionReceipts, receiptData(&receipts[i]))
	}
	return nil
}
func canonicalReceiptsTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, requested any) ([]types.ReceiptData, error) {
	values, ok := requested.([]interface{})
	if !ok || len(values) > 20 {
		return nil, errArgument
	}
	rows, err := s.ActionReceiptsModel.ListByRunTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	result := []types.ReceiptData{}
	for i := range rows {
		known[rows[i].ActionId] = true
		result = append(result, receiptData(&rows[i]))
	}
	for _, item := range values {
		data, ok := item.(map[string]any)
		if !ok {
			return nil, errArgument
		}
		id, _ := data["action_id"].(string)
		if !known[id] {
			return nil, errForbidden
		}
	}
	return result, nil
}
