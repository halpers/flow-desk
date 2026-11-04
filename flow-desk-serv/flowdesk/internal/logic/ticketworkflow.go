package logic

import (
	"context"
	"database/sql"
	"encoding/json"
	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"strings"
	"unicode/utf8"
)

const ticketStartTool = "ticket.start_processing"
const ticketResolveTool = "ticket.resolve"
const ticketGraphVersion = "ticket-v2"

type ticketWorkflowInput struct {
	TicketId        string `json:"ticket_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
	Content         string `json:"content"`
}

func validateWorkflow(actor *model.Users, ticket *model.Tickets, tool string) error {
	if !actor.Enabled || !permitted(actor, "ticket:manage") {
		return errForbidden
	}
	required := stateOpen
	if tool == ticketResolveTool {
		required = stateInProgress
	}
	if ticket.Status != required {
		return failure(409, "INVALID_TRANSITION", "当前工单状态不支持此操作，请重新查询")
	}
	if ticket.AssigneeUserId.Valid && ticket.AssigneeUserId.String != actor.Id && actor.Role != "admin" {
		return failure(409, "TICKET_ASSIGNED", "工单由其他成员负责，请先联系主管调整指派")
	}
	if tool == ticketResolveTool && !ticket.AssigneeUserId.Valid && actor.Role != "admin" {
		return failure(409, "TICKET_ASSIGNED", "请先接手工单再提交处理结论")
	}
	return nil
}

func prepareExistingTicketTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, tool, arguments string) (*model.Tickets, error) {
	var input ticketWorkflowInput
	if json.Unmarshal([]byte(arguments), &input) != nil || !validID(input.TicketId) || input.ExpectedVersion < 1 {
		return nil, errArgument
	}
	text := input.Reason
	if tool == ticketNoteTool {
		text = input.Content
	}
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxTicketText {
		return nil, errArgument
	}
	ticket, err := s.TicketsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, input.TicketId)
	if err != nil {
		return nil, err
	}
	if err = teamAccessTx(ctx, s, tx, actor, ticket.TeamId); err != nil {
		return nil, err
	}
	if ticket.Version != input.ExpectedVersion {
		return nil, failure(409, "RESOURCE_CHANGED", "工单版本已变化，请重新生成方案")
	}
	if tool == ticketNoteTool {
		if ticket.Status == stateClosed {
			return nil, failure(409, "TICKET_CLOSED", "已关闭工单不能添加备注")
		}
	} else if err = validateWorkflow(actor, ticket, tool); err != nil {
		return nil, err
	}
	return ticket, nil
}

func ticketWorkflowTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, action *model.Actions) (types.TicketData, string, error) {
	var result types.TicketData
	ticket, err := prepareExistingTicketTx(ctx, s, tx, actor, action.ToolName, action.Arguments)
	if err != nil {
		return result, "", err
	}
	var input ticketWorkflowInput
	if err = json.Unmarshal([]byte(action.Arguments), &input); err != nil {
		return result, "", err
	}
	noteID := ""
	operation := "ticket.processing_started"
	if action.ToolName == ticketStartTool {
		ticket.Status = stateInProgress
		ticket.AssigneeUserId = sql.NullString{String: actor.Id, Valid: true}
	} else {
		ticket.Status = stateResolved
		operation = "ticket.resolved"
		note := &model.TicketNotes{Id: orderedID(), TenantId: actor.TenantId, TicketId: ticket.Id, Content: input.Reason, CreatedBy: actor.Id, SourceActionId: sql.NullString{String: action.Id, Valid: true}}
		if err = s.TicketNotesModel.InsertTx(ctx, tx, note); err != nil {
			return result, "", err
		}
		noteID = note.Id
	}
	if err = s.TicketsModel.UpdateStateTx(ctx, tx, ticket); err != nil {
		return result, "", err
	}
	ticket.Version++
	if err = recordTicketTx(ctx, s, tx, actor, ticket.Id, operation, input.Reason, action.Id); err != nil {
		return result, "", err
	}
	return ticketData(ticket), noteID, nil
}
