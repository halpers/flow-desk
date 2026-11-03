package logic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
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

func conversationData(row *model.Conversations) types.ConversationData {
	return types.ConversationData{Id: row.Id, Title: row.Title, Status: row.Status, Version: row.Version, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}
func messageData(row *model.Messages) types.MessageData {
	citations := []map[string]any{}
	receipts := []map[string]any{}
	_ = json.Unmarshal([]byte(row.Citations), &citations)
	_ = json.Unmarshal([]byte(row.ActionReceipts), &receipts)
	return types.MessageData{Id: row.Id, RunId: row.RunId, Role: row.Role, Content: row.Content, Status: row.Status, Generation: row.Generation, Position: row.Position, Citations: citations, ActionReceipts: receipts, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}
func runDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.Runs) (types.RunData, error) {
	message, err := s.MessagesModel.FindOneTx(ctx, tx, row.TenantId, row.ResultMessageId)
	if err != nil {
		return types.RunData{}, err
	}
	visible := messageData(message)
	if err = redactMessageTx(ctx, s, tx, row, &visible); err != nil {
		return types.RunData{}, err
	}
	result := types.RunData{Id: row.Id, ConversationId: row.ConversationId, Status: row.Status, Version: row.Version, LastEventSeq: row.LastEventSeq, ErrorCode: row.ErrorCode, ToolCallCount: row.ToolCallCount, CurrentMessage: visible}
	if err = addRunControlsTx(ctx, s, tx, row, &result); err != nil {
		return types.RunData{}, err
	}
	return result, nil
}

func CreateConversation(ctx context.Context, s *svc.ServiceContext, req *types.CreateConversationReq) (types.ConversationData, error) {
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "新会话"
	}
	if utf8.RuneCountInString(req.Title) > 120 {
		return types.ConversationData{}, errArgument
	}
	return idempotent(ctx, s, "conversation.create", req.IdempotencyKey, req, 201, func(ctx context.Context, tx sqlx.Session) (types.ConversationData, error) {
		actor := Current(ctx).User
		row := &model.Conversations{Id: uuid.NewString(), TenantId: actor.TenantId, OwnerUserId: actor.Id, Title: req.Title, Status: "active", Version: 1}
		if err := s.ConversationsModel.InsertTx(ctx, tx, row); err != nil {
			return types.ConversationData{}, err
		}
		persisted, err := s.ConversationsModel.FindOneOwnedForUpdateTx(ctx, tx, actor.TenantId, actor.Id, row.Id)
		if err != nil {
			return types.ConversationData{}, err
		}
		return conversationData(persisted), nil
	})
}

func ListConversations(ctx context.Context, s *svc.ServiceContext, req *types.PageReq) (types.ConversationPage, error) {
	result := types.ConversationPage{Items: []types.ConversationData{}}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	actor := current.User
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	scope := "conversations:" + actor.TenantId + ":" + actor.Id
	position, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	before := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	beforeID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if position != "" {
		parts := strings.Split(position, "|")
		if len(parts) != 2 || !validID(parts[1]) {
			return result, errArgument
		}
		before, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return result, errArgument
		}
		beforeID = parts[1]
	}
	rows, err := s.ConversationsModel.ListOwned(ctx, actor.TenantId, actor.Id, before, beforeID, limit+1)
	if err != nil {
		return result, err
	}
	result.HasMore = int64(len(rows)) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for index := range rows {
		item := conversationData(&rows[index])
		active, err := s.RunsModel.FindActiveByConversation(ctx, actor.TenantId, item.Id)
		if err == nil {
			item.ActiveRunId = active.Id
		} else if !errors.Is(err, model.ErrNotFound) {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	if result.HasMore {
		last := rows[len(rows)-1]
		result.NextCursor = encodeCursor(scope, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"|"+last.Id)
	}
	return result, nil
}

func GetConversation(ctx context.Context, s *svc.ServiceContext, id string) (types.ConversationData, error) {
	if !validID(id) {
		return types.ConversationData{}, errArgument
	}
	current, err := identityRequired(ctx)
	if err != nil {
		return types.ConversationData{}, err
	}
	row, err := s.ConversationsModel.FindOneOwned(ctx, current.User.TenantId, current.User.Id, id)
	if err != nil {
		return types.ConversationData{}, err
	}
	result := conversationData(row)
	active, err := s.RunsModel.FindActiveByConversation(ctx, current.User.TenantId, id)
	if err == nil {
		result.ActiveRunId = active.Id
	} else if !errors.Is(err, model.ErrNotFound) {
		return result, err
	}
	return result, nil
}

func ListMessages(ctx context.Context, s *svc.ServiceContext, req *types.MessagesReq) (types.MessagePage, error) {
	result := types.MessagePage{Items: []types.MessageData{}}
	if _, err := GetConversation(ctx, s, req.ConversationId); err != nil {
		return result, err
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	actor := Current(ctx).User
	scope := "messages:" + actor.TenantId + ":" + actor.Id + ":" + req.ConversationId
	position, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	after := int64(0)
	if position != "" {
		after, err = strconv.ParseInt(position, 10, 64)
		if err != nil || after < 0 {
			return result, errArgument
		}
	}
	rows, err := s.MessagesModel.ListByConversation(ctx, actor.TenantId, req.ConversationId, after, limit+1)
	if err != nil {
		return result, err
	}
	result.HasMore = int64(len(rows)) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for index := range rows {
		message := messageData(&rows[index])
		if len(message.Citations) > 0 {
			err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
				run, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, actor.TenantId, actor.Id, message.RunId)
				if err != nil {
					return err
				}
				return redactMessageTx(ctx, s, tx, run, &message)
			})
			if err != nil {
				return result, err
			}
		}
		result.Items = append(result.Items, message)
	}
	if result.HasMore {
		result.NextCursor = encodeCursor(scope, strconv.FormatInt(rows[len(rows)-1].Position, 10))
	}
	return result, nil
}

func GetRun(ctx context.Context, s *svc.ServiceContext, id string) (types.RunData, error) {
	var result types.RunData
	if !validID(id) {
		return result, errArgument
	}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		// Writers lock the run before touching its message or sequence; this share lock makes the snapshot consistent.
		row, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, current.User.TenantId, current.User.Id, id)
		if err != nil {
			return err
		}
		result, err = runDataTx(ctx, s, tx, row)
		return err
	})
	return result, err
}

func SendMessage(ctx context.Context, s *svc.ServiceContext, req *types.SendMessageReq) (types.RunData, error) {
	req.Content = strings.TrimSpace(req.Content)
	if !validID(req.ConversationId) || !validID(req.ClientMessageId) || utf8.RuneCountInString(req.Content) < 1 || utf8.RuneCountInString(req.Content) > 8000 || len(req.Attachments) > 0 {
		return types.RunData{}, errArgument
	}
	return idempotent(ctx, s, "message.send:"+req.ConversationId, req.IdempotencyKey, req, 202, func(ctx context.Context, tx sqlx.Session) (types.RunData, error) {
		actor := Current(ctx).User
		conversation, err := s.ConversationsModel.FindOneOwnedForUpdateTx(ctx, tx, actor.TenantId, actor.Id, req.ConversationId)
		if err != nil {
			return types.RunData{}, err
		}
		if conversation.Status != "active" {
			return types.RunData{}, errForbidden
		}
		previous, err := s.MessagesModel.FindOneByClientMessageTx(ctx, tx, actor.TenantId, conversation.Id, req.ClientMessageId)
		if err == nil {
			if previous.Content != req.Content {
				return types.RunData{}, failure(409, "IDEMPOTENCY_CONFLICT", "同一消息标识的内容已经改变")
			}
			run, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, actor.TenantId, actor.Id, previous.RunId)
			if err != nil {
				return types.RunData{}, err
			}
			return runDataTx(ctx, s, tx, run)
		}
		if !errors.Is(err, model.ErrNotFound) {
			return types.RunData{}, err
		}
		active, err := s.RunsModel.FindActiveByConversationTx(ctx, tx, actor.TenantId, conversation.Id)
		if err == nil {
			busy := failure(409, "CONVERSATION_BUSY", "当前会话还有任务正在处理")
			busy.Data = map[string]any{"active_run_id": active.Id}
			return types.RunData{}, busy
		}
		if !errors.Is(err, model.ErrNotFound) {
			return types.RunData{}, err
		}
		now := time.Now().UTC()
		if err = s.RunsModel.LockTenantQuotaTx(ctx, tx, actor.TenantId); err != nil {
			return types.RunData{}, err
		}
		tenantCount, actorCount, err := s.RunsModel.CountActiveTx(ctx, tx, actor.TenantId, actor.Id)
		if err != nil {
			return types.RunData{}, err
		}
		if tenantCount >= maxTenantActiveRuns || actorCount >= maxActorActiveRuns {
			return types.RunData{}, failure(429, "QUEUE_FULL", "当前正在处理的任务较多，请稍后重试")
		}
		run := &model.Runs{Id: uuid.NewString(), TenantId: actor.TenantId, ConversationId: conversation.Id, ActorUserId: actor.Id, SessionId: Current(ctx).Session.Id, InputMessageId: uuid.NewString(), ResultMessageId: uuid.NewString(), DispatchId: uuid.NewString(), Status: domain.RunQueued, Version: 1, LeaseGeneration: 1, DeadlineAt: now.Add(runDeadline), ResumeData: "{}"}
		if err = s.RunsModel.InsertTx(ctx, tx, run); err != nil {
			return types.RunData{}, err
		}
		userMessage := &model.Messages{Id: run.InputMessageId, TenantId: actor.TenantId, ConversationId: conversation.Id, RunId: run.Id, Role: "user", Position: conversation.MessageCount + 1, ClientMessageId: sql.NullString{String: req.ClientMessageId, Valid: true}, Content: req.Content, Status: "final", Generation: 1, Citations: "[]", ActionReceipts: "[]"}
		assistant := &model.Messages{Id: run.ResultMessageId, TenantId: actor.TenantId, ConversationId: conversation.Id, RunId: run.Id, Role: "assistant", Position: conversation.MessageCount + 2, Status: "streaming", Generation: 1, Citations: "[]", ActionReceipts: "[]"}
		if err = s.MessagesModel.InsertTx(ctx, tx, userMessage); err != nil {
			return types.RunData{}, err
		}
		if err = s.MessagesModel.InsertTx(ctx, tx, assistant); err != nil {
			return types.RunData{}, err
		}
		if err = s.ConversationsModel.UpdateMessageCountTx(ctx, tx, conversation.Id, conversation.MessageCount); err != nil {
			return types.RunData{}, err
		}
		payload := map[string]any{"status": domain.RunQueued, "run_version": run.Version + 1}
		if _, err = appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.NameSpaceURL, []byte(run.Id+":queued")).String(), domain.EventRunQueued, payload, now, ""); err != nil {
			return types.RunData{}, err
		}
		command := map[string]any{"dispatch_id": run.DispatchId, "run_id": run.Id, "lease_generation": run.LeaseGeneration, "command": "execute", "graph_version": "ticket-v1"}
		if err = s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: uuid.NewString(), TenantId: actor.TenantId, AggregateId: run.Id, DispatchId: run.DispatchId, Topic: "run.execute", Payload: marshal(command), Status: "pending", AvailableAt: now}); err != nil {
			return types.RunData{}, err
		}
		return runDataTx(ctx, s, tx, run)
	})
}
