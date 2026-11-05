package logic

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const capabilityLifetime = 60 * time.Second
const maxRunEvents int64 = 50000

var errorCodePattern = regexp.MustCompile(`^[A-Z_]{1,80}$`)

type runCapability struct {
	jwt.RegisteredClaims
	RunId       string   `json:"run_id"`
	TenantId    string   `json:"tenant_id"`
	ActorUserId string   `json:"actor_user_id"`
	SessionId   string   `json:"session_id"`
	Generation  int64    `json:"generation"`
	Scopes      []string `json:"scopes"`
}

func parseCapability(s *svc.ServiceContext, raw string) (*runCapability, error) {
	claims := &runCapability{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (interface{}, error) { return []byte(s.Config.JwtSecret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid || !claims.VerifyIssuer("flowdesk", true) || !claims.VerifyAudience("go-tools", true) || claims.ExpiresAt == nil {
		return nil, errForbidden
	}
	return claims, nil
}
func hasScope(claims *runCapability, scope string) bool {
	for _, item := range claims.Scopes {
		if item == scope {
			return true
		}
	}
	return false
}

func authorizeRunTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs) error {
	if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, run.TenantId); err != nil {
		return err
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
	if err != nil {
		return err
	}
	if !actor.Enabled || actor.TenantId != run.TenantId || !permitted(actor, "conversation:use") {
		return errForbidden
	}
	active, err := s.SessionsModel.ExistsLiveFamilyBySessionTx(ctx, tx, run.TenantId, run.ActorUserId, run.SessionId)
	if err != nil {
		return err
	}
	if !active {
		return errForbidden
	}
	return nil
}
func authorizeCapabilityTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, claims *runCapability, scope string) error {
	if claims.RunId != run.Id || claims.TenantId != run.TenantId || claims.ActorUserId != run.ActorUserId || claims.SessionId != run.SessionId || claims.Generation != run.LeaseGeneration || !hasScope(claims, scope) {
		return errStale
	}
	if run.Status != domain.RunQueued && run.Status != domain.RunRunning {
		return errStale
	}
	if !run.LeaseExpiresAt.Valid || time.Now().After(run.LeaseExpiresAt.Time) || time.Now().After(run.DeadlineAt) {
		return errStale
	}
	return authorizeRunTx(ctx, s, tx, run)
}

func RenewLease(ctx context.Context, s *svc.ServiceContext, req *types.LeaseReq) (types.ExecutionContext, error) {
	var result types.ExecutionContext
	if !validID(req.RunId) || !validID(req.DispatchId) || req.LeaseGeneration < 1 {
		return result, errArgument
	}
	existing, err := s.RunsModel.FindOne(ctx, req.RunId)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, existing.TenantId, req.RunId)
		if err != nil {
			return err
		}
		if run.DispatchId != req.DispatchId || run.LeaseGeneration != req.LeaseGeneration || (run.Status != domain.RunQueued && run.Status != domain.RunRunning) || time.Now().After(run.DeadlineAt) {
			return errStale
		}
		if err = authorizeRunTx(ctx, s, tx, run); err != nil {
			return err
		}
		visible, err := runTicketsVisibleTx(ctx, s, tx, run)
		if err != nil {
			return err
		}
		if !visible {
			return errMissing
		}
		if err = s.RunsModel.UpdateLeaseTx(ctx, tx, run.Id, run.LeaseGeneration); err != nil {
			return err
		}
		input, err := s.MessagesModel.FindOneTx(ctx, tx, run.TenantId, run.InputMessageId)
		if err != nil {
			return err
		}
		expires := time.Now().Add(capabilityLifetime)
		if expires.After(run.DeadlineAt) {
			expires = run.DeadlineAt
		}
		scopes := []string{domain.ToolOrderLookup, domain.ToolLogisticsTrack, ticketSearchTool, "event:append", "knowledge:read", actionPrepareScope, actionExecuteScope}
		claims := runCapability{RegisteredClaims: jwt.RegisteredClaims{Issuer: "flowdesk", Subject: run.ActorUserId, Audience: jwt.ClaimStrings{"go-tools"}, ExpiresAt: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(time.Now()), ID: uuid.NewString()}, RunId: run.Id, TenantId: run.TenantId, ActorUserId: run.ActorUserId, SessionId: run.SessionId, Generation: run.LeaseGeneration, Scopes: scopes}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.Config.JwtSecret))
		if err != nil {
			return err
		}
		actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
		if err != nil {
			return err
		}
		tools := []string{}
		if permitted(actor, "order:read") {
			tools = append(tools, domain.ToolOrderLookup, domain.ToolLogisticsTrack)
			if permitted(actor, "ticket:read") {
				tools = append(tools, ticketSearchTool)
			}
		}
		for _, tool := range []string{ticketCreateTool, ticketNoteTool, ticketStartTool, ticketResolveTool} {
			if permitted(actor, actionPermission(tool)) {
				tools = append(tools, tool)
			}
		}
		result = types.ExecutionContext{RunId: run.Id, TenantId: run.TenantId, ActorUserId: run.ActorUserId, LeaseGeneration: run.LeaseGeneration, Capability: token, Content: input.Content, MessageId: run.ResultMessageId, DeadlineAt: run.DeadlineAt.UTC().Format(time.RFC3339Nano), Tools: tools, ResumeData: jsonObject(run.ResumeData)}
		return nil
	})
	return result, err
}

func appendEventTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, sourceID, kind string, payload map[string]any, occurred time.Time, sourceHash string) (int64, error) {
	if run.LastEventSeq >= maxRunEvents {
		return 0, failure(429, "BUDGET_EXCEEDED", "任务事件已达到上限")
	}
	seq := run.LastEventSeq + 1
	frame := types.EventData{SchemaVersion: "1", RunId: run.Id, Seq: seq, Type: kind, OccurredAt: occurred.UTC().Format(time.RFC3339Nano), Payload: payload}
	// Normalize numeric representations to the JSON data model expected by the validator.
	var value any
	if err := json.Unmarshal([]byte(marshal(frame)), &value); err != nil {
		return 0, err
	}
	if err := domain.ValidateEvent(value); err != nil {
		return 0, errArgument
	}
	if sourceHash == "" {
		sourceHash = HashToken(marshal(frame))
	}
	row := &model.RunEvents{Id: uuid.NewString(), TenantId: run.TenantId, RunId: run.Id, Seq: seq, SourceEventId: sourceID, PayloadHash: sourceHash, Type: kind, Payload: marshal(payload), OccurredAt: occurred}
	if err := s.RunEventsModel.InsertTx(ctx, tx, row); err != nil {
		return 0, err
	}
	run.LastEventSeq = seq
	if err := s.RunsModel.UpdateProgressTx(ctx, tx, run); err != nil {
		return 0, err
	}
	run.Version++
	return seq, nil
}

func IngestEvent(ctx context.Context, s *svc.ServiceContext, req *types.AgentEventReq, rawCapability string) (types.EventAck, error) {
	var result types.EventAck
	if req.Type == "resource.revoked" {
		return result, errArgument
	}
	if !validID(req.RunId) || !validID(req.SourceEventId) || req.LeaseGeneration < 1 {
		return result, errArgument
	}
	occurred, err := time.Parse(time.RFC3339Nano, req.OccurredAt)
	if err != nil {
		return result, errArgument
	}
	existing, err := s.RunsModel.FindOne(ctx, req.RunId)
	if err != nil {
		return result, err
	}
	originalHash := HashToken(marshal(req))
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, existing.TenantId, req.RunId)
		if err != nil {
			return err
		}
		previous, err := s.RunEventsModel.FindOneBySourceTx(ctx, tx, run.TenantId, run.Id, req.SourceEventId)
		if err == nil {
			if previous.PayloadHash != originalHash {
				return failure(409, "EVENT_CONTENT_CONFLICT", "相同事件标识的内容已经改变")
			}
			result.Seq = previous.Seq
			return nil
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		claims, err := parseCapability(s, rawCapability)
		if err != nil {
			return err
		}
		if req.LeaseGeneration != run.LeaseGeneration {
			return errStale
		}
		if err = authorizeCapabilityTx(ctx, s, tx, run, claims, "event:append"); err != nil {
			return err
		}
		if occurred.After(time.Now().Add(5*time.Second)) || occurred.Before(run.CreatedAt.Add(-5*time.Second)) {
			return errArgument
		}
		// Keep the original callback immutable so replay hashes are stable for every caller.
		var payload map[string]any
		if err = json.Unmarshal([]byte(marshal(req.Payload)), &payload); err != nil {
			return err
		}
		if payload == nil {
			payload = map[string]any{}
		}
		switch req.Type {
		case domain.EventRunStarted:
			if run.Status != domain.RunQueued {
				return errStale
			}
			run.Status = domain.RunRunning
			payload["status"] = run.Status
			payload["run_version"] = run.Version + 1
		case domain.EventToolStarted, domain.EventToolCompleted:
			if run.Status != domain.RunRunning {
				return errStale
			}
		case domain.EventRunWaitingApproval, domain.EventRunWaitingInput:
			if err = registerWaitTx(ctx, s, tx, run, req.Type, payload); err != nil {
				return err
			}
		case domain.EventMessageCompleted:
			if run.Status != domain.RunRunning || payload["message_id"] != run.ResultMessageId {
				return errStale
			}
			receipts, err := canonicalReceiptsTx(ctx, s, tx, run, payload["action_receipts"])
			if err != nil {
				return err
			}
			payload["action_receipts"] = receipts
			content, ok := payload["content"].(string)
			if !ok {
				return errArgument
			}
			citations, err := canonicalCitationsTx(ctx, s, tx, run, payload["citations"])
			if err != nil {
				return err
			}
			payload["citations"] = citations
			if err = s.MessagesModel.UpdateFinalTx(ctx, tx, run.ResultMessageId, content, marshal(citations), marshal(receipts)); err != nil {
				return err
			}
		case domain.EventRunSucceeded:
			if run.Status != domain.RunRunning {
				return errStale
			}
			message, err := s.MessagesModel.FindOneTx(ctx, tx, run.TenantId, run.ResultMessageId)
			if err != nil {
				return err
			}
			if message.Status != "final" || message.Content == "" {
				return errStale
			}
			run.Status = domain.RunSucceeded
			payload["status"] = run.Status
			payload["run_version"] = run.Version + 1
		case domain.EventRunFailed:
			code, ok := payload["error_code"].(string)
			if !ok || !errorCodePattern.MatchString(code) {
				return errArgument
			}
			run.Status = domain.RunFailed
			run.ErrorCode = code
			payload["status"] = run.Status
			payload["run_version"] = run.Version + 1
			if err = s.MessagesModel.UpdateAbortedTx(ctx, tx, run.ResultMessageId); err != nil {
				return err
			}
		default:
			return errArgument
		}
		result.Seq, err = appendEventTx(ctx, s, tx, run, req.SourceEventId, req.Type, payload, occurred, originalHash)
		return err
	})
	return result, err
}

func ListRunEvents(ctx context.Context, s *svc.ServiceContext, runID string, after int64) ([]types.EventData, error) {
	snapshot, err := GetRun(ctx, s, runID)
	if err != nil {
		return nil, err
	}
	if after < 0 || after > snapshot.LastEventSeq {
		return nil, errArgument
	}
	rows, err := s.RunEventsModel.ListAfterSequence(ctx, Current(ctx).User.TenantId, runID, after, 100)
	if err != nil {
		return nil, err
	}
	events := []types.EventData{}
	for _, row := range rows {
		if row.Seq > snapshot.LastEventSeq {
			break
		}
		var payload map[string]any
		if err = json.Unmarshal([]byte(row.Payload), &payload); err != nil {
			return nil, err
		}
		if row.Type == domain.EventMessageCompleted {
			payload["content"] = snapshot.CurrentMessage.Content
			payload["citations"] = snapshot.CurrentMessage.Citations
			payload["action_receipts"] = snapshot.CurrentMessage.ActionReceipts
		}
		if snapshot.CurrentMessage.Content == resourceHiddenMessage {
			row.Type = "resource.revoked"
			payload = map[string]any{"summary": resourceHiddenMessage}
		}
		events = append(events, types.EventData{SchemaVersion: "1", RunId: row.RunId, Seq: row.Seq, Type: row.Type, OccurredAt: row.OccurredAt.UTC().Format(time.RFC3339Nano), Payload: payload})
	}
	return events, nil
}

func CallTool(ctx context.Context, s *svc.ServiceContext, req *types.ToolReq, rawCapability string) (types.ToolResult, error) {
	result := types.ToolResult{Logistics: []types.LogisticsData{}, Tickets: []types.TicketData{}}
	var toolError error
	if err := domain.ValidateTool(map[string]any{"tool_name": req.ToolName, "arguments": req.Arguments}); err != nil {
		return result, errArgument
	}
	if req.ToolName != domain.ToolOrderLookup && req.ToolName != domain.ToolLogisticsTrack && req.ToolName != ticketSearchTool {
		return result, errArgument
	}
	if len(req.Arguments) != 1 {
		return result, errArgument
	}
	number, ok := req.Arguments["order_no"].(string)
	if !ok || len(number) < 4 || len(number) > 50 {
		return result, errArgument
	}
	claims, err := parseCapability(s, rawCapability)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, claims.TenantId, claims.RunId)
		if err != nil {
			return err
		}
		if run.Status != domain.RunRunning {
			return errStale
		}
		if err = authorizeCapabilityTx(ctx, s, tx, run, claims, req.ToolName); err != nil {
			return err
		}
		actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
		if err != nil {
			return err
		}
		if !permitted(actor, "order:read") {
			return errForbidden
		}
		if req.ToolName == ticketSearchTool && !permitted(actor, "ticket:read") {
			return errForbidden
		}
		if err = s.RunsModel.UpdateToolCountTx(ctx, tx, run.Id); err != nil {
			return failure(429, "BUDGET_EXCEEDED", "工具调用次数已达到上限")
		}
		order, err := s.OrdersModel.FindOneByOrderNoTx(ctx, tx, run.TenantId, number)
		if errors.Is(err, model.ErrNotFound) {
			toolError = errMissing
			return nil
		}
		if err != nil {
			return err
		}
		if req.ToolName == domain.ToolOrderLookup {
			result.Order = orderData(order)
		} else if req.ToolName == domain.ToolLogisticsTrack {
			rows, err := s.LogisticsEventsModel.ListByOrderTx(ctx, tx, run.TenantId, order.Id)
			if err != nil {
				return err
			}
			result.Logistics = logisticsData(rows)
		} else {
			if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, run.TenantId); err != nil {
				return err
			}
			rows, err := s.TicketsModel.ListAccessibleTx(ctx, tx, run.TenantId, actor.Id, actor.Role, order.Id, "", "", 20)
			if err != nil {
				return err
			}
			for i := range rows {
				if err = rememberTicketTx(ctx, s, tx, run, rows[i].Id); err != nil {
					return err
				}
				result.Tickets = append(result.Tickets, ticketData(&rows[i]))
			}
		}
		return nil
	})
	if err == nil {
		err = toolError
	}
	return result, err
}

func DispatchNext(ctx context.Context, s *svc.ServiceContext) (bool, error) {
	var event *model.OutboxEvents
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		event, err = s.OutboxEventsModel.FindClaimableForUpdateTx(ctx, tx)
		if err != nil {
			return err
		}
		if err = s.OutboxEventsModel.UpdateClaimTx(ctx, tx, event.Id); err != nil {
			return err
		}
		event.AttemptCount++
		return nil
	})
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if event.Topic == "knowledge.archive" {
		err = ArchiveOriginalSource(ctx, s, event)
	} else if event.Topic == "knowledge.index" {
		err = s.AgentClient.DispatchKnowledge(ctx, event.Payload)
	} else if event.Topic == "run.cancel" {
		err = s.AgentClient.Cancel(ctx, event.AggregateId, event.Payload)
	} else {
		err = s.AgentClient.Dispatch(ctx, event.Payload)
	}
	if err != nil {
		if event.Topic == "knowledge.archive" {
			return true, s.OutboxEventsModel.UpdateRetry(ctx, event.Id, event.AttemptCount, "SOURCE_ARCHIVE_UNAVAILABLE")
		}
		return true, s.OutboxEventsModel.UpdateRetry(ctx, event.Id, event.AttemptCount, "AGENT_UNAVAILABLE")
	}
	return true, s.OutboxEventsModel.UpdateDelivered(ctx, event.Id, event.AttemptCount)
}

func ExpireOverdueRuns(ctx context.Context, s *svc.ServiceContext) error {
	rows, err := s.RunsModel.ListOverdue(ctx, 10)
	if err != nil {
		return err
	}
	for _, row := range rows {
		err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
			run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, row.TenantId, row.Id)
			if err != nil {
				return err
			}
			waiting := run.Status == domain.RunWaitingInput || run.Status == domain.RunWaitingApproval
			if domain.IsTerminal(run.Status) || (!waiting && time.Now().Before(run.DeadlineAt)) || (waiting && (!run.WaitExpiresAt.Valid || time.Now().Before(run.WaitExpiresAt.Time))) {
				return nil
			}
			if err = invalidatePendingTx(ctx, s, tx, run, "expired"); err != nil {
				return err
			}
			run.Status = domain.RunExpired
			run.ErrorCode = "EXECUTION_TIMEOUT"
			if waiting {
				run.ErrorCode = "WAIT_TIMEOUT"
			}
			if err = s.MessagesModel.UpdateAbortedTx(ctx, tx, run.ResultMessageId); err != nil {
				return err
			}
			_, err = appendEventTx(ctx, s, tx, run, uuid.NewSHA1(uuid.NameSpaceURL, []byte(run.Id+":expired")).String(), domain.EventRunExpired, map[string]any{"status": run.Status, "run_version": run.Version + 1, "error_code": run.ErrorCode}, time.Now().UTC(), "")
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
