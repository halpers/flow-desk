package logic

import (
	"context"
	"database/sql"
	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"strings"
	"unicode/utf8"
)

const maxTeamsPerTenant int64 = 50
const maxDirectoryUsers = 500
const resourceHiddenMessage = "此任务使用的工单访问权限已变化，请重新查询。"

func teamData(row *model.Teams) types.TeamData {
	return types.TeamData{Id: row.Id, Name: row.Name, Revision: row.Revision}
}
func ListTeams(ctx context.Context, s *svc.ServiceContext) (types.TeamPage, error) {
	result := types.TeamPage{Items: []types.TeamData{}}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	rows, err := s.TeamsModel.ListAccessible(ctx, current.User.TenantId, current.User.Id, current.User.Role)
	for i := range rows {
		result.Items = append(result.Items, teamData(&rows[i]))
	}
	return result, err
}
func recordTeamAuditTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, operation, kind, id, summary string) error {
	return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: operation, ObjectType: kind, ObjectId: id, Source: "manual", Summary: summary})
}
func CreateTeam(ctx context.Context, s *svc.ServiceContext, req *types.CreateTeamReq) (types.TeamData, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.TeamData{}, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 80 {
		return types.TeamData{}, errArgument
	}
	return idempotent(ctx, s, "team.create", req.IdempotencyKey, req, 201, func(ctx context.Context, tx sqlx.Session) (types.TeamData, error) {
		if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, actor.TenantId); err != nil {
			return types.TeamData{}, err
		}
		count, err := s.TeamsModel.CountByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.TeamData{}, err
		}
		if count >= maxTeamsPerTenant {
			return types.TeamData{}, failure(429, "TEAM_LIMIT", "团队数量已达到上限")
		}
		row := &model.Teams{Id: orderedID(), TenantId: actor.TenantId, Name: req.Name, Revision: 1}
		if err = s.TeamsModel.InsertTx(ctx, tx, row); err != nil {
			return types.TeamData{}, err
		}
		err = recordTeamAuditTx(ctx, s, tx, actor, "team.created", "team", row.Id, row.Name)
		return teamData(row), err
	})
}
func teamUsers(rows []model.TeamUser) (types.TeamUserPage, error) {
	result := types.TeamUserPage{Items: []types.TeamUserData{}}
	if len(rows) > maxDirectoryUsers {
		return result, failure(429, "DIRECTORY_LIMIT", "账号目录超过演示上限")
	}
	for _, row := range rows {
		result.Items = append(result.Items, types.TeamUserData{Id: row.Id, DisplayName: row.DisplayName, Role: row.Role, Enabled: row.Enabled})
	}
	return result, nil
}
func ListTeamUsers(ctx context.Context, s *svc.ServiceContext) (types.TeamUserPage, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.TeamUserPage{}, err
	}
	rows, err := s.UsersModel.ListTeamDirectory(ctx, actor.TenantId)
	if err != nil {
		return types.TeamUserPage{}, err
	}
	return teamUsers(rows)
}
func ListTeamMembers(ctx context.Context, s *svc.ServiceContext, req *types.TeamReq) (types.TeamUserPage, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.TeamUserPage{}, err
	}
	if !validID(req.TeamId) {
		return types.TeamUserPage{}, errArgument
	}
	if _, err = s.TeamsModel.FindOneByTenantIdId(ctx, actor.TenantId, req.TeamId); err != nil {
		return types.TeamUserPage{}, err
	}
	rows, err := s.TeamMembersModel.ListUsers(ctx, actor.TenantId, req.TeamId)
	if err != nil {
		return types.TeamUserPage{}, err
	}
	return teamUsers(rows)
}
func UpdateTeamMembership(ctx context.Context, s *svc.ServiceContext, req *types.TeamMembershipReq) (types.TeamData, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.TeamData{}, err
	}
	if !validID(req.TeamId) || !validID(req.UserId) || req.ExpectedRevision < 1 {
		return types.TeamData{}, errArgument
	}
	return idempotent(ctx, s, "team.member:"+req.TeamId+":"+req.UserId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.TeamData, error) {
		if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, actor.TenantId); err != nil {
			return types.TeamData{}, err
		}
		row, err := s.TeamsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.TeamId)
		if err != nil {
			return types.TeamData{}, err
		}
		if row.Revision != req.ExpectedRevision {
			return types.TeamData{}, failure(409, "VERSION_CONFLICT", "团队成员已变化，请刷新")
		}
		user, err := s.UsersModel.FindOneTx(ctx, tx, req.UserId)
		if err != nil {
			return types.TeamData{}, err
		}
		if user.TenantId != actor.TenantId || (req.Member && !user.Enabled) {
			return types.TeamData{}, errMissing
		}
		if req.Member {
			err = s.TeamMembersModel.InsertIfAbsentTx(ctx, tx, &model.TeamMembers{Id: orderedID(), TenantId: actor.TenantId, TeamId: row.Id, UserId: user.Id})
		} else {
			err = s.TeamMembersModel.DeleteMembershipTx(ctx, tx, actor.TenantId, row.Id, user.Id)
		}
		if err != nil {
			return types.TeamData{}, err
		}
		if err = s.TeamsModel.UpdateRevisionTx(ctx, tx, row); err != nil {
			return types.TeamData{}, err
		}
		row.Revision++
		err = recordTeamAuditTx(ctx, s, tx, actor, "team.membership", "team", row.Id, marshal(map[string]any{"user_id": user.Id, "member": req.Member, "revision": row.Revision}))
		return teamData(row), err
	})
}
func teamAccessTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, team sql.NullString) error {
	if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, actor.TenantId); err != nil {
		return err
	}
	if !actor.Enabled {
		return errForbidden
	}
	if !team.Valid || actor.Role == "admin" {
		return nil
	}
	exists, err := s.TeamMembersModel.ExistsMembershipTx(ctx, tx, actor.TenantId, team.String, actor.Id)
	if err != nil {
		return err
	}
	if !exists {
		return errMissing
	}
	return nil
}
func ticketAccessTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, id string) (*model.Tickets, error) {
	row, err := s.TicketsModel.FindOneForShareTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return nil, err
	}
	if !permitted(actor, "ticket:read") {
		return nil, errForbidden
	}
	return row, teamAccessTx(ctx, s, tx, actor, row.TeamId)
}
func validateTeamTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, tenant, team string) error {
	if team == "" {
		return nil
	}
	if !validID(team) {
		return errArgument
	}
	_, err := s.TeamsModel.FindOneForUpdateTx(ctx, tx, tenant, team)
	return err
}
func SetTicketTeam(ctx context.Context, s *svc.ServiceContext, req *types.TicketTeamReq) (types.TicketData, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.TicketData{}, err
	}
	if !validID(req.TicketId) || req.ExpectedVersion < 1 {
		return types.TicketData{}, errArgument
	}
	return idempotentTicket(ctx, s, "ticket.team:"+req.TicketId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.TicketData, error) {
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
		if err = validateTeamTx(ctx, s, tx, actor.TenantId, req.TeamId); err != nil {
			return types.TicketData{}, err
		}
		row.TeamId = sql.NullString{String: req.TeamId, Valid: req.TeamId != ""}
		if row.TeamId.Valid && row.AssigneeUserId.Valid {
			member, err := s.TeamMembersModel.ExistsMembershipTx(ctx, tx, actor.TenantId, req.TeamId, row.AssigneeUserId.String)
			if err != nil {
				return types.TicketData{}, err
			}
			if !member {
				row.AssigneeUserId = sql.NullString{}
			}
		}
		if err = s.TicketsModel.UpdateTeamTx(ctx, tx, row); err != nil {
			return types.TicketData{}, err
		}
		row.Version++
		err = recordTicketTx(ctx, s, tx, actor, row.Id, "ticket.team", marshal(map[string]string{"team_id": req.TeamId}), "")
		return ticketData(row), err
	})
}
func SetKnowledgeTeam(ctx context.Context, s *svc.ServiceContext, req *types.KnowledgeTeamReq) (types.KnowledgeBaseData, error) {
	actor, err := requirePermission(ctx, "team:manage")
	if err != nil {
		return types.KnowledgeBaseData{}, err
	}
	if !validID(req.KnowledgeBaseId) || req.ExpectedRevision < 1 {
		return types.KnowledgeBaseData{}, errArgument
	}
	return idempotent(ctx, s, "knowledge.team:"+req.KnowledgeBaseId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session) (types.KnowledgeBaseData, error) {
		row, err := s.KnowledgeBasesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.KnowledgeBaseId)
		if err != nil {
			return types.KnowledgeBaseData{}, err
		}
		if err = teamAccessTx(ctx, s, tx, actor, row.TeamId); err != nil {
			return types.KnowledgeBaseData{}, err
		}
		if row.AclRevision != req.ExpectedRevision {
			return types.KnowledgeBaseData{}, failure(409, "VERSION_CONFLICT", "知识库权限版本已变化")
		}
		if err = validateTeamTx(ctx, s, tx, actor.TenantId, req.TeamId); err != nil {
			return types.KnowledgeBaseData{}, err
		}
		row.TeamId = sql.NullString{String: req.TeamId, Valid: req.TeamId != ""}
		if err = s.KnowledgeBasesModel.UpdateTeamTx(ctx, tx, row); err != nil {
			return types.KnowledgeBaseData{}, err
		}
		row.AclRevision++
		err = recordTeamAuditTx(ctx, s, tx, actor, "knowledge.team", "knowledge_base", row.Id, marshal(map[string]string{"team_id": req.TeamId}))
		return baseData(row), err
	})
}
func idempotentTicket(ctx context.Context, s *svc.ServiceContext, operation, key string, req any, status int, work func(context.Context, sqlx.Session) (types.TicketData, error)) (types.TicketData, error) {
	return idempotentAuthorized(ctx, s, operation, key, req, status, work, func(ctx context.Context, tx sqlx.Session, result *types.TicketData) error {
		_, err := ticketAccessTx(ctx, s, tx, Current(ctx).User, result.Id)
		return err
	})
}
func rememberTicketTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, ticket string) error {
	return s.RunTicketResourcesModel.InsertIfAbsentTx(ctx, tx, &model.RunTicketResources{Id: orderedID(), TenantId: run.TenantId, RunId: run.Id, TicketId: ticket})
}
func runTicketsVisibleTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs) (bool, error) {
	resources, err := s.RunTicketResourcesModel.ListTicketIdsTx(ctx, tx, run.TenantId, run.Id)
	if err != nil {
		return false, err
	}
	if len(resources) == 0 {
		return true, nil
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
	if err != nil {
		return false, err
	}
	if !actor.Enabled || !permitted(actor, "ticket:read") {
		return false, nil
	}
	if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, run.TenantId); err != nil {
		return false, err
	}
	denied, err := s.RunTicketResourcesModel.ExistsInaccessibleTx(ctx, tx, run.TenantId, run.Id, actor.Id, actor.Role)
	return !denied, err
}
func idempotentRun(ctx context.Context, s *svc.ServiceContext, operation, key string, req any, status int, work func(context.Context, sqlx.Session) (types.RunData, error)) (types.RunData, error) {
	return idempotentAuthorized(ctx, s, operation, key, req, status, work, func(ctx context.Context, tx sqlx.Session, result *types.RunData) error {
		actor := Current(ctx).User
		run, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, actor.TenantId, actor.Id, result.Id)
		if err != nil {
			return err
		}
		*result, err = runDataTx(ctx, s, tx, run)
		return err
	})
}
