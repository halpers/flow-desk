package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type TeamMembersModel interface {
	teamMembersModel
	ExistsMembershipTx(context.Context, sqlx.Session, string, string, string) (bool, error)
	InsertIfAbsentTx(context.Context, sqlx.Session, *TeamMembers) error
	DeleteMembershipTx(context.Context, sqlx.Session, string, string, string) error
	ListUsers(context.Context, string, string) ([]TeamUser, error)
}
type TeamUser struct {
	Id          string `db:"id"`
	DisplayName string `db:"display_name"`
	Role        string `db:"role"`
	Enabled     bool   `db:"enabled"`
}
type customTeamMembersModel struct{ *defaultTeamMembersModel }

func NewTeamMembersModel(conn sqlx.SqlConn) TeamMembersModel {
	return &customTeamMembersModel{newTeamMembersModel(conn)}
}
func (m *customTeamMembersModel) ExistsMembershipTx(ctx context.Context, tx sqlx.Session, tenant, team, user string) (bool, error) {
	var exists bool
	err := tx.QueryRowCtx(ctx, &exists, fmt.Sprintf("select exists(select 1 from %s where tenant_id=$1 and team_id=$2 and user_id=$3)", m.table), tenant, team, user)
	return exists, err
}
func (m *customTeamMembersModel) InsertIfAbsentTx(ctx context.Context, tx sqlx.Session, row *TeamMembers) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("insert into %s(id,tenant_id,team_id,user_id) values($1,$2,$3,$4) on conflict(tenant_id,team_id,user_id) do nothing", m.table), row.Id, row.TenantId, row.TeamId, row.UserId)
	return err
}
func (m *customTeamMembersModel) DeleteMembershipTx(ctx context.Context, tx sqlx.Session, tenant, team, user string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("delete from %s where tenant_id=$1 and team_id=$2 and user_id=$3", m.table), tenant, team, user)
	return err
}
func (m *customTeamMembersModel) ListUsers(ctx context.Context, tenant, team string) ([]TeamUser, error) {
	rows := []TeamUser{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select u.id,u.display_name,u.role,u.enabled from %s tm join business.users u on u.tenant_id=tm.tenant_id and u.id=tm.user_id where tm.tenant_id=$1 and tm.team_id=$2 order by u.display_name,u.id limit 501", m.table), tenant, team)
	return rows, err
}
