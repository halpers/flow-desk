package logic

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const waitLifetime = 24 * time.Hour
const actionPrepareScope = "action:prepare"
const actionExecuteScope = "action:execute"

func jsonObject(raw string) map[string]any {
	result := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}
func approvalData(row *model.Approvals) types.ApprovalData {
	return types.ApprovalData{Id: row.Id, RunId: row.RunId, ActionId: row.ActionId, Status: row.Status, Version: row.Version, PayloadDigest: row.PayloadDigest, DisplaySnapshot: jsonObject(row.DisplaySnapshot), ExpiresAt: row.ExpiresAt.UTC().Format(time.RFC3339Nano), DecisionReason: row.DecisionReason}
}
func receiptData(row *model.ActionReceipts) types.ReceiptData {
	return types.ReceiptData{ActionId: row.ActionId, ToolName: row.ToolName, BusinessObjectType: row.BusinessObjectType, BusinessObjectId: row.BusinessObjectId, Result: jsonObject(row.Result), ExecutedAt: row.ExecutedAt.UTC().Format(time.RFC3339Nano)}
}
func evidenceValues(ids []string) []interface{} {
	values := []interface{}{}
	for _, id := range ids {
		values = append(values, map[string]any{"id": id})
	}
	return values
}
func actionDigest(tool, arguments, resources, evidence string) string {
	var evidenceValue any
	_ = json.Unmarshal([]byte(evidence), &evidenceValue)
	// JSONB can reorder object keys; hash the normalized JSON data model.
	return HashToken(marshal(map[string]any{"tool_name": tool, "tool_version": "1", "arguments": jsonObject(arguments), "resource_versions": jsonObject(resources), "evidence_versions": evidenceValue}))
}
func actionPermission(tool string) string {
	if tool == ticketCreateTool {
		return "ticket:create"
	}
	if tool == ticketNoteTool {
		return "ticket:note"
	}
	if tool == ticketStartTool || tool == ticketResolveTool {
		return "ticket:manage"
	}
	return "invalid"
}

func PrepareAction(ctx context.Context, s *svc.ServiceContext, req *types.PrepareActionReq, raw string) (types.ApprovalData, error) {
	var result types.ApprovalData
	if !validID(req.RunId) || !validID(req.ActionId) || len(req.EvidenceIds) > 5 {
		return result, errArgument
	}
	var proposal any
	_ = json.Unmarshal([]byte(marshal(map[string]any{"tool_name": req.ToolName, "arguments": req.Arguments})), &proposal)
	if err := domain.ValidateAction(proposal); err != nil {
		return result, errArgument
	}
	claims, err := parseCapability(s, raw)
	if err != nil {
		return result, err
	}
	if claims.RunId != req.RunId {
		return result, errForbidden
	}
	requestHash := HashToken(marshal(req))
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, claims.TenantId, req.RunId)
		if err != nil {
			return err
		}
		if err = authorizeCapabilityTx(ctx, s, tx, run, claims, actionPrepareScope); err != nil {
			return err
		}
		if run.Status != domain.RunRunning {
			return errStale
		}
		actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
		if err != nil {
			return err
		}
		if !permitted(actor, actionPermission(req.ToolName)) {
			return errForbidden
		}
		previous, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, req.ActionId)
		if err == nil {
			if previous.RunId != run.Id || previous.RequestHash != requestHash {
				return failure(409, "ACTION_CONTENT_CONFLICT", "相同动作标识的内容已变化")
			}
			approval, err := s.ApprovalsModel.FindOneByActionForUpdateTx(ctx, tx, run.TenantId, previous.Id)
			if err != nil {
				return err
			}
			result = approvalData(approval)
			var citations []interface{}
			if err = json.Unmarshal([]byte(previous.EvidenceVersions), &citations); err != nil {
				return err
			}
			if _, err = canonicalCitationsTx(ctx, s, tx, run, citations); err != nil {
				return err
			}
			visible, err := runTicketsVisibleTx(ctx, s, tx, run)
			if err != nil {
				return err
			}
			if !visible {
				return errMissing
			}
			return nil
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		actions, err := s.ActionsModel.ListByRunTx(ctx, tx, run.TenantId, run.Id)
		if err != nil {
			return err
		}
		if len(actions) > 0 {
			return failure(409, "ACTION_LIMIT", "每次任务只办理一个动作，请重新发起任务")
		}
		citations, err := canonicalCitationsTx(ctx, s, tx, run, evidenceValues(req.EvidenceIds))
		if err != nil {
			return err
		}
		arguments := marshal(req.Arguments)
		resources := map[string]any{}
		display := map[string]any{"tool_name": req.ToolName, "arguments": req.Arguments, "evidence": citations}
		if req.ToolName == ticketCreateTool {
			var input types.CreateTicketReq
			if json.Unmarshal([]byte(arguments), &input) != nil {
				return errArgument
			}
			if err = validateTicket(&input); err != nil {
				return err
			}
			order, err := s.OrdersModel.FindOneForShareTx(ctx, tx, run.TenantId, input.OrderId)
			if err != nil {
				return err
			}
			if order.Version != input.ExpectedVersion {
				return failure(409, "RESOURCE_CHANGED", "订单版本已变化")
			}
			if order.Status == stateCancelled {
				return failure(409, "ORDER_CANCELLED", "已取消订单不能创建售后工单")
			}
			resources["order"] = order.Version
			display["order_no"] = order.OrderNo
		} else {
			ticket, err := prepareExistingTicketTx(ctx, s, tx, actor, req.ToolName, arguments)
			if err != nil {
				return err
			}
			if err = rememberTicketTx(ctx, s, tx, run, ticket.Id); err != nil {
				return err
			}
			resources["ticket"] = ticket.Version
			display["ticket_no"] = ticket.TicketNo
			if req.ToolName == ticketStartTool || req.ToolName == ticketResolveTool {
				display["from_status"] = ticket.Status
				display["to_status"] = stateInProgress
				if req.ToolName == ticketResolveTool {
					display["to_status"] = stateResolved
				}
				display["actor_display_name"] = actor.DisplayName
			}
		}
		expires := time.Now().UTC().Add(waitLifetime).Truncate(time.Microsecond)
		display["expires_at"] = expires.Format(time.RFC3339Nano)
		action := &model.Actions{Id: req.ActionId, TenantId: run.TenantId, RunId: run.Id, ToolName: req.ToolName, ToolVersion: "1", Arguments: arguments, RequestHash: requestHash, ResourceVersions: marshal(resources), EvidenceVersions: marshal(citations), Status: statePrepared, ExpiresAt: expires, Version: 1}
		action.PayloadDigest = actionDigest(action.ToolName, action.Arguments, action.ResourceVersions, action.EvidenceVersions)
		if err = s.ActionsModel.InsertTx(ctx, tx, action); err != nil {
			return err
		}
		approval := &model.Approvals{Id: uuid.NewSHA1(uuid.MustParse(action.Id), []byte("approval")).String(), TenantId: run.TenantId, RunId: run.Id, ActionId: action.Id, RequesterUserId: actor.Id, Status: statePrepared, PayloadDigest: action.PayloadDigest, DisplaySnapshot: marshal(display), ExpiresAt: expires, Version: 1}
		if err = s.ApprovalsModel.InsertTx(ctx, tx, approval); err != nil {
			return err
		}
		result = approvalData(approval)
		return nil
	})
	return result, err
}
func ListApprovals(ctx context.Context, s *svc.ServiceContext, runID string) (types.ApprovalPage, error) {
	result := types.ApprovalPage{Items: []types.ApprovalData{}}
	snapshot, err := GetRun(ctx, s, runID)
	if err != nil {
		return result, err
	}
	result.Items = snapshot.Approvals
	return result, nil
}
func receiptForRunTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, actionID string) (types.ReceiptData, error) {
	row, err := s.ActionReceiptsModel.FindOneByActionTx(ctx, tx, run.TenantId, actionID)
	if err != nil {
		return types.ReceiptData{}, err
	}
	action, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, actionID)
	if err != nil {
		return types.ReceiptData{}, err
	}
	if action.RunId != run.Id {
		return types.ReceiptData{}, errForbidden
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
	if err != nil {
		return types.ReceiptData{}, err
	}
	if !actor.Enabled || !permitted(actor, "ticket:read") {
		return types.ReceiptData{}, errForbidden
	}
	if err = authorizeRunTx(ctx, s, tx, run); err != nil {
		return types.ReceiptData{}, err
	}
	if _, err = ticketAccessTx(ctx, s, tx, actor, row.BusinessObjectId); err != nil {
		return types.ReceiptData{}, err
	}
	return receiptData(row), nil
}
func GetActionReceipt(ctx context.Context, s *svc.ServiceContext, req *types.ActionReq, raw string) (types.ReceiptData, error) {
	return executeAction(ctx, s, req, raw, false)
}
func ExecuteAction(ctx context.Context, s *svc.ServiceContext, req *types.ActionReq, raw string) (types.ReceiptData, error) {
	return executeAction(ctx, s, req, raw, true)
}
func executeAction(ctx context.Context, s *svc.ServiceContext, req *types.ActionReq, raw string, execute bool) (types.ReceiptData, error) {
	var result types.ReceiptData
	if !validID(req.RunId) || !validID(req.ActionId) {
		return result, errArgument
	}
	claims, err := parseCapability(s, raw)
	if err != nil {
		return result, err
	}
	if claims.RunId != req.RunId || !hasScope(claims, actionExecuteScope) {
		return result, errForbidden
	}
	var committedError error
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, claims.TenantId, req.RunId)
		if err != nil {
			return err
		}
		if run.ActorUserId != claims.ActorUserId || run.SessionId != claims.SessionId {
			return errForbidden
		}
		prior, err := receiptForRunTx(ctx, s, tx, run, req.ActionId)
		if err == nil {
			result = prior
			return nil
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		if !execute {
			return errMissing
		}
		if err = authorizeCapabilityTx(ctx, s, tx, run, claims, actionExecuteScope); err != nil {
			return err
		}
		if run.Status != domain.RunRunning {
			return errStale
		}
		action, err := s.ActionsModel.FindOneForUpdateTx(ctx, tx, run.TenantId, req.ActionId)
		if err != nil {
			return err
		}
		if action.RunId != run.Id {
			return errForbidden
		}
		approval, err := s.ApprovalsModel.FindOneByActionForUpdateTx(ctx, tx, run.TenantId, action.Id)
		if err != nil {
			return err
		}
		if action.Status != statePrepared || approval.Status != stateApproved || approval.RequesterUserId != run.ActorUserId || !approval.DecidedBy.Valid || approval.DecidedBy.String != run.ActorUserId || approval.PayloadDigest != action.PayloadDigest || action.PayloadDigest != actionDigest(action.ToolName, action.Arguments, action.ResourceVersions, action.EvidenceVersions) {
			return failure(409, "APPROVAL_REQUIRED", "此动作没有有效的精确批准")
		}
		actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
		if err != nil {
			return err
		}
		if !permitted(actor, actionPermission(action.ToolName)) {
			return errForbidden
		}
		invalidate := func(cause error) error {
			committedError = cause
			return invalidateActionTx(ctx, s, tx, run, action, approval)
		}
		if time.Now().After(action.ExpiresAt) || time.Now().After(approval.ExpiresAt) {
			return invalidate(failure(410, "APPROVAL_EXPIRED", "确认已过期，请重新生成方案"))
		}
		var citations []interface{}
		if err = json.Unmarshal([]byte(action.EvidenceVersions), &citations); err != nil {
			return err
		}
		if _, err = canonicalCitationsTx(ctx, s, tx, run, citations); err != nil {
			return invalidate(err)
		}
		var ticket types.TicketData
		noteID := ""
		if action.ToolName == ticketCreateTool {
			var input types.CreateTicketReq
			if err = json.Unmarshal([]byte(action.Arguments), &input); err != nil {
				return err
			}
			ticket, err = createTicketTx(ctx, s, tx, actor, &input, action.Id)
		} else if action.ToolName == ticketStartTool || action.ToolName == ticketResolveTool {
			ticket, noteID, err = ticketWorkflowTx(ctx, s, tx, actor, action)
		} else {
			var input types.AddNoteReq
			if err = json.Unmarshal([]byte(action.Arguments), &input); err != nil {
				return err
			}
			input.TicketId, _ = jsonObject(action.Arguments)["ticket_id"].(string)
			ticket, noteID, err = addNoteTx(ctx, s, tx, actor, &input, action.Id)
		}
		if err != nil {
			var business *BusinessError
			if errors.As(err, &business) && (business.Code == "RESOURCE_CHANGED" || business.Code == "ORDER_CANCELLED" || business.Code == "TICKET_CLOSED" || business.Code == "INVALID_TRANSITION" || business.Code == "TICKET_ASSIGNED" || business.Code == "RESOURCE_NOT_FOUND" || business.Code == "PERMISSION_DENIED") {
				return invalidate(err)
			}
			return err
		}
		values := map[string]any{"ticket_id": ticket.Id, "ticket_no": ticket.TicketNo, "ticket_version": ticket.Version}
		if action.ToolName == ticketStartTool || action.ToolName == ticketResolveTool {
			values["status"] = ticket.Status
			values["assignee_user_id"] = ticket.AssigneeUserId
		}
		if err = rememberTicketTx(ctx, s, tx, run, ticket.Id); err != nil {
			return err
		}
		if noteID != "" {
			values["note_id"] = noteID
		}
		receipt := &model.ActionReceipts{Id: uuid.NewString(), TenantId: run.TenantId, ActionId: action.Id, ToolName: action.ToolName, BusinessObjectType: "ticket", BusinessObjectId: ticket.Id, Result: marshal(values), ExecutedBy: actor.Id, ExecutedAt: time.Now().UTC().Truncate(time.Microsecond)}
		if err = s.ActionReceiptsModel.InsertTx(ctx, tx, receipt); err != nil {
			return err
		}
		if err = s.ActionsModel.UpdateStatusTx(ctx, tx, run.TenantId, action.Id, stateExecuted); err != nil {
			return err
		}
		result = receiptData(receipt)
		if err = saveVisibleReceiptsTx(ctx, s, tx, run); err != nil {
			return err
		}
		_, err = appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.MustParse(action.Id), []byte(stateExecuted)).String(), domain.EventActionExecuted, map[string]any{"receipt": result}, time.Now().UTC(), "")
		return err
	})
	if err == nil {
		err = committedError
	}
	return result, err
}
func invalidateActionTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, action *model.Actions, approval *model.Approvals) error {
	if err := s.ActionsModel.UpdateStatusTx(ctx, tx, run.TenantId, action.Id, stateInvalidated); err != nil {
		return err
	}
	approval.Status = stateInvalidated
	if err := s.ApprovalsModel.UpdateDecisionTx(ctx, tx, approval); err != nil {
		return err
	}
	// The worker reports a typed failure; immutable proposal history remains reviewable.
	return nil
}
func saveVisibleReceiptsTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs) error {
	receipts, err := s.ActionReceiptsModel.ListByRunTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return err
	}
	values := []types.ReceiptData{}
	for i := range receipts {
		values = append(values, receiptData(&receipts[i]))
	}
	return s.MessagesModel.UpdateReceiptsTx(ctx, tx, run.ResultMessageId, marshal(values))
}
