package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type TeamsModel interface {
	teamsModel
	InsertTx(context.Context, sqlx.Session, *Teams) error
	ListAccessible(context.Context, string, string, string) ([]Teams, error)
	FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*Teams, error)
	UpdateRevisionTx(context.Context, sqlx.Session, *Teams) error
	LockAuthorizationForShareTx(context.Context, sqlx.Session, string) error
	LockAuthorizationForUpdateTx(context.Context, sqlx.Session, string) error
	CountByTenantTx(context.Context, sqlx.Session, string) (int64, error)
}
type customTeamsModel struct{ *defaultTeamsModel }

func NewTeamsModel(conn sqlx.SqlConn) TeamsModel { return &customTeamsModel{newTeamsModel(conn)} }
func (m *customTeamsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Teams) error {
	_, err := newTeamsModel(sqlx.NewSqlConnFromSession(tx)).Insert(ctx, row)
	return err
}
func (m *customTeamsModel) ListAccessible(ctx context.Context, tenant, actor, role string) ([]Teams, error) {
	rows := []Teams{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf(`select %s from %s where tenant_id=$1 and ($3='admin' or exists (select 1 from business.team_members tm where tm.tenant_id=teams.tenant_id and tm.team_id=teams.id and tm.user_id=$2)) order by name,id limit 51`, teamsRows, m.table), tenant, actor, role)
	return rows, err
}
func (m *customTeamsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Teams, error) {
	var row Teams
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", teamsRows, m.table), tenant, id)
	return &row, err
}
func (m *customTeamsModel) UpdateRevisionTx(ctx context.Context, tx sqlx.Session, row *Teams) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set revision=revision+1 where tenant_id=$1 and id=$2 and revision=$3", m.table), row.TenantId, row.Id, row.Revision))
}

// Membership writers never lock tickets, runs or knowledge resources. This lock
// fences revocation against all resource checks until their transaction commits.
func (m *customTeamsModel) LockAuthorizationForShareTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	return lockAuthorizationForShareTx(ctx, tx, tenant)
}
func (m *customTeamsModel) LockAuthorizationForUpdateTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	_, err := tx.ExecCtx(ctx, "select pg_advisory_xact_lock(hashtextextended($1,64))", tenant)
	return err
}
func (m *customTeamsModel) CountByTenantTx(ctx context.Context, tx sqlx.Session, tenant string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1", m.table), tenant)
	return count, err
}
