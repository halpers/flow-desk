package logic

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const ticketCreateTool = "ticket.create"
const ticketNoteTool = "ticket.add_note"
const ticketSearchTool = "ticket.search"
const maxTicketText = 4000

func orderedID() string { return uuid.Must(uuid.NewV7()).String() }
func ticketData(row *model.Tickets) types.TicketData {
	return types.TicketData{TeamId: row.TeamId.String, Id: row.Id, TicketNo: row.TicketNo, OrderId: row.OrderId, Title: row.Title, Description: row.Description, Category: row.Category, Priority: row.Priority, Status: row.Status, AssigneeUserId: row.AssigneeUserId.String, Version: row.Version, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}
func requirePermission(ctx context.Context, permission string) (*model.Users, error) {
	current, err := identityRequired(ctx)
	if err != nil {
		return nil, err
	}
	if !current.User.Enabled || !permitted(current.User, permission) {
		return nil, errForbidden
	}
	return current.User, nil
}
func validateTicket(req *types.CreateTicketReq) error {
	if !validID(req.OrderId) || req.ExpectedVersion < 1 || strings.TrimSpace(req.Title) == "" || utf8.RuneCountInString(req.Title) > 120 || strings.TrimSpace(req.Description) == "" || utf8.RuneCountInString(req.Description) > maxTicketText || !slices.Contains([]string{"after_sales", "logistics", "other"}, req.Category) || !slices.Contains([]string{"normal", "urgent"}, req.Priority) {
		return errArgument
	}
	return nil
}
func recordTicketTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, ticketID, operation, summary, actionID string) error {
	source := "manual"
	action := sql.NullString{}
	if actionID != "" {
		source = "agent"
		action = sql.NullString{String: actionID, Valid: true}
	}
	if err := s.TicketEventsModel.InsertTx(ctx, tx, &model.TicketEvents{Id: orderedID(), TenantId: actor.TenantId, TicketId: ticketID, EventType: operation, ActorUserId: actor.Id, Summary: summary, ActionId: action}); err != nil {
		return err
	}
	return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: operation, ObjectType: "ticket", ObjectId: ticketID, Source: source, ActionId: action, Summary: summary})
}
func createTicketTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, req *types.CreateTicketReq, actionID string) (types.TicketData, error) {
	var result types.TicketData
	if !actor.Enabled || !permitted(actor, "ticket:create") {
		return result, errForbidden
	}
	if err := validateTicket(req); err != nil {
		return result, err
	}
	order, err := s.OrdersModel.FindOneForShareTx(ctx, tx, actor.TenantId, req.OrderId)
	if err != nil {
		return result, err
	}
	if order.Version != req.ExpectedVersion {
		return result, failure(409, "RESOURCE_CHANGED", "订单版本已变化，请重新确认")
	}
	if order.Status == stateCancelled {
		return result, failure(409, "ORDER_CANCELLED", "已取消订单不能创建售后工单")
	}
	id := orderedID()
	row := &model.Tickets{Id: id, TenantId: actor.TenantId, TicketNo: "T" + strings.ReplaceAll(id, "-", ""), OrderId: order.Id, Title: req.Title, Description: req.Description, Category: req.Category, Priority: req.Priority, Status: stateOpen, CreatedBy: actor.Id, Version: 1}
	if actionID != "" {
		row.SourceActionId = sql.NullString{String: actionID, Valid: true}
	}
	if err = s.TicketsModel.InsertTx(ctx, tx, row); err != nil {
		return result, err
	}
	if err = recordTicketTx(ctx, s, tx, actor, id, "ticket.created", "创建工单："+row.Title, actionID); err != nil {
		return result, err
	}
	row, err = s.TicketsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return result, err
	}
	return ticketData(row), nil
}
func addNoteTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, req *types.AddNoteReq, actionID string) (types.TicketData, string, error) {
	var result types.TicketData
	if !actor.Enabled || !permitted(actor, "ticket:note") {
		return result, "", errForbidden
	}
	if !validID(req.TicketId) || req.ExpectedVersion < 1 || strings.TrimSpace(req.Content) == "" || utf8.RuneCountInString(req.Content) > maxTicketText {
		return result, "", errArgument
	}
	row, err := s.TicketsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.TicketId)
	if err != nil {
		return result, "", err
	}
	if err = teamAccessTx(ctx, s, tx, actor, row.TeamId); err != nil {
		return result, "", err
	}
	if row.Version != req.ExpectedVersion {
		return result, "", failure(409, "RESOURCE_CHANGED", "工单版本已变化，请重新确认")
	}
	if row.Status == stateClosed {
		return result, "", failure(409, "TICKET_CLOSED", "已关闭工单不能添加备注")
	}
	note := &model.TicketNotes{Id: orderedID(), TenantId: actor.TenantId, TicketId: row.Id, Content: req.Content, CreatedBy: actor.Id}
	if actionID != "" {
		note.SourceActionId = sql.NullString{String: actionID, Valid: true}
	}
	if err = s.TicketNotesModel.InsertTx(ctx, tx, note); err != nil {
		return result, "", err
	}
	if err = s.TicketsModel.UpdateStateTx(ctx, tx, row); err != nil {
		return result, "", err
	}
	row.Version++
	if err = recordTicketTx(ctx, s, tx, actor, row.Id, "ticket.note_added", req.Content, actionID); err != nil {
		return result, "", err
	}
	return ticketData(row), note.Id, nil
}
func CreateTicket(ctx context.Context, s *svc.ServiceContext, req *types.CreateTicketReq) (types.TicketData, error) {
	actor, err := requirePermission(ctx, "ticket:create")
	if err != nil {
		return types.TicketData{}, err
	}
	return idempotentTicket(ctx, s, "ticket.create", req.IdempotencyKey, req, 201, func(ctx context.Context, tx sqlx.Session) (types.TicketData, error) {
		return createTicketTx(ctx, s, tx, actor, req, "")
	})
}
func AddTicketNote(ctx context.Context, s *svc.ServiceContext, req *types.AddNoteReq) (types.TicketData, error) {
	actor, err := requirePermission(ctx, "ticket:note")
	if err != nil {
		return types.TicketData{}, err
	}
	return idempotentTicket(ctx, s, "ticket.note:"+req.TicketId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.TicketData, error) {
		row, _, err := addNoteTx(ctx, s, tx, actor, req, "")
		return row, err
	})
}
func GetTicket(ctx context.Context, s *svc.ServiceContext, id string) (types.TicketData, error) {
	actor, err := requirePermission(ctx, "ticket:read")
	if err != nil {
		return types.TicketData{}, err
	}
	if !validID(id) {
		return types.TicketData{}, errArgument
	}
	var result types.TicketData
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := ticketAccessTx(ctx, s, tx, actor, id)
		if err == nil {
			result = ticketData(row)
		}
		return err
	})
	return result, err
}
func ListTickets(ctx context.Context, s *svc.ServiceContext, req *types.TicketsReq) (types.TicketPage, error) {
	result := types.TicketPage{Items: []types.TicketData{}}
	actor, err := requirePermission(ctx, "ticket:read")
	if err != nil {
		return result, err
	}
	if (req.OrderId != "" && !validID(req.OrderId)) || (req.Status != "" && !slices.Contains([]string{stateOpen, stateInProgress, stateResolved, stateClosed}, req.Status)) {
		return result, errArgument
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	scope := "tickets:" + actor.TenantId + ":" + actor.Id + ":" + req.OrderId + ":" + req.Status
	before, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
			return err
		}
		rows, err := s.TicketsModel.ListAccessibleTx(ctx, tx, actor.TenantId, actor.Id, actor.Role, req.OrderId, req.Status, before, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for i := range rows {
			result.Items = append(result.Items, ticketData(&rows[i]))
		}
		if result.HasMore {
			result.NextCursor = encodeCursor(scope, rows[len(rows)-1].TicketNo)
		}
		return nil
	})
	return result, err
}
func changeTicket(ctx context.Context, s *svc.ServiceContext, req *types.TicketChangeReq, assignment bool) (types.TicketData, error) {
	actor, err := requirePermission(ctx, "ticket:manage")
	if err != nil {
		return types.TicketData{}, err
	}
	if !validID(req.TicketId) || req.ExpectedVersion < 1 {
		return types.TicketData{}, errArgument
	}
	operation := "ticket.status"
	if assignment {
		operation = "ticket.assignment"
	}
	return idempotentTicket(ctx, s, operation+":"+req.TicketId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.TicketData, error) {
		row, err := s.TicketsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.TicketId)
		if err != nil {
			return types.TicketData{}, err
		}
		if err = teamAccessTx(ctx, s, tx, actor, row.TeamId); err != nil {
			return types.TicketData{}, err
		}
		if row.Version != req.ExpectedVersion {
			return types.TicketData{}, failure(409, "RESOURCE_CHANGED", "工单版本已变化")
		}
		summary := ""
		if assignment {
			if req.Status != "" || !validID(req.AssigneeUserId) {
				return types.TicketData{}, errArgument
			}
			assignee, err := s.UsersModel.FindOneTx(ctx, tx, req.AssigneeUserId)
			if err != nil {
				return types.TicketData{}, err
			}
			if !assignee.Enabled || assignee.TenantId != actor.TenantId {
				return types.TicketData{}, errForbidden
			}
			if err = teamAccessTx(ctx, s, tx, assignee, row.TeamId); err != nil {
				return types.TicketData{}, err
			}
			row.AssigneeUserId = sql.NullString{String: assignee.Id, Valid: true}
			summary = "负责人：" + assignee.DisplayName
		} else {
			allowed := map[string][]string{stateOpen: {stateInProgress, stateClosed}, stateInProgress: {stateResolved, stateClosed}, stateResolved: {stateInProgress, stateClosed}}
			if req.AssigneeUserId != "" || !slices.Contains(allowed[row.Status], req.Status) {
				return types.TicketData{}, failure(409, "INVALID_TRANSITION", "工单状态变更不允许")
			}
			summary = row.Status + " → " + req.Status
			row.Status = req.Status
		}
		if err = s.TicketsModel.UpdateStateTx(ctx, tx, row); err != nil {
			return types.TicketData{}, err
		}
		row.Version++
		if err = recordTicketTx(ctx, s, tx, actor, row.Id, operation, summary, ""); err != nil {
			return types.TicketData{}, err
		}
		return ticketData(row), nil
	})
}
func AssignTicket(ctx context.Context, s *svc.ServiceContext, req *types.TicketChangeReq) (types.TicketData, error) {
	return changeTicket(ctx, s, req, true)
}
func ChangeTicketStatus(ctx context.Context, s *svc.ServiceContext, req *types.TicketChangeReq) (types.TicketData, error) {
	return changeTicket(ctx, s, req, false)
}
func ListTicketEvents(ctx context.Context, s *svc.ServiceContext, req *types.TimelineReq) (types.TimelinePage, error) {
	result := types.TimelinePage{Items: []types.TimelineData{}}
	if _, err := GetTicket(ctx, s, req.TicketId); err != nil {
		return result, err
	}
	actor := Current(ctx).User
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	scope := "timeline:" + actor.TenantId + ":" + req.TicketId
	after, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if _, err := ticketAccessTx(ctx, s, tx, actor, req.TicketId); err != nil {
			return err
		}
		rows, err := s.TicketEventsModel.ListByTicketTx(ctx, tx, actor.TenantId, req.TicketId, after, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			result.Items = append(result.Items, types.TimelineData{Id: row.Id, Operation: row.EventType, Summary: row.Summary, ActorUserId: row.ActorUserId, ObjectId: row.TicketId, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
		}
		if result.HasMore {
			result.NextCursor = encodeCursor(scope, rows[len(rows)-1].Id)
		}
		return nil
	})
	return result, err
}
func ListAuditEvents(ctx context.Context, s *svc.ServiceContext, req *types.PageReq) (types.TimelinePage, error) {
	result := types.TimelinePage{Items: []types.TimelineData{}}
	actor, err := requirePermission(ctx, "audit:read")
	if err != nil {
		return result, err
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	scope := "audit:" + actor.TenantId
	before, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		rows, err := s.AuditEventsModel.ListTenantTx(ctx, tx, actor.TenantId, before, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			result.Items = append(result.Items, types.TimelineData{Id: row.Id, Operation: row.Operation, Summary: row.Summary, ActorUserId: row.ActorUserId, ObjectId: row.ObjectId, Source: row.Source, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
		}
		if result.HasMore {
			result.NextCursor = encodeCursor(scope, rows[len(rows)-1].Id)
		}
		return nil
	})
	return result, err
}
