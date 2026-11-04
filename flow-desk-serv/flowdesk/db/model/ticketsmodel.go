package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type TicketsModel interface {
	ticketsModel
	withSession(sqlx.Session) TicketsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *Tickets) error
	FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Tickets, error)
	FindOneByTenant(ctx context.Context, tenant, id string) (*Tickets, error)
	FindOneForShareTx(context.Context, sqlx.Session, string, string) (*Tickets, error)
	ListAccessibleTx(context.Context, sqlx.Session, string, string, string, string, string, string, int64) ([]Tickets, error)
	UpdateTeamTx(context.Context, sqlx.Session, *Tickets) error
	ListTenantTx(ctx context.Context, tx sqlx.Session, tenant, order, status, before string, limit int64) ([]Tickets, error)
	UpdateStateTx(ctx context.Context, tx sqlx.Session, row *Tickets) error
}
type customTicketsModel struct{ *defaultTicketsModel }

func NewTicketsModel(conn sqlx.SqlConn) TicketsModel {
	return &customTicketsModel{newTicketsModel(conn)}
}
func (m *customTicketsModel) withSession(tx sqlx.Session) TicketsModel {
	return NewTicketsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customTicketsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Tickets) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customTicketsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Tickets, error) {
	var row Tickets
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", ticketsRows, m.table), tenant, id)
	return &row, err
}
func (m *customTicketsModel) FindOneByTenant(ctx context.Context, tenant, id string) (*Tickets, error) {
	var row Tickets
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2", ticketsRows, m.table), tenant, id)
	return &row, err
}
func (m *customTicketsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Tickets, error) {
	var row Tickets
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", ticketsRows, m.table), tenant, id)
	return &row, err
}
func (m *customTicketsModel) ListAccessibleTx(ctx context.Context, tx sqlx.Session, tenant, actor, role, order, status, before string, limit int64) ([]Tickets, error) {
	rows := []Tickets{}
	query := fmt.Sprintf("select %s from %s where tenant_id=$1 and ($2='' or order_id::text=$2) and ($3='' or status=$3) and ($4='' or ticket_no<$4) and %s order by ticket_no desc limit $5 for share", ticketsRows, m.table, fmt.Sprintf(teamAccessPredicate, 6, "tickets", 7))
	err := tx.QueryRowsCtx(ctx, &rows, query, tenant, order, status, before, limit, role, actor)
	return rows, err
}
func (m *customTicketsModel) UpdateTeamTx(ctx context.Context, tx sqlx.Session, row *Tickets) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set team_id=$3,assignee_user_id=$4,version=version+1,updated_at=now() where tenant_id=$1 and id=$2 and version=$5", m.table), row.TenantId, row.Id, row.TeamId, row.AssigneeUserId, row.Version))
}
func (m *customTicketsModel) ListTenantTx(ctx context.Context, tx sqlx.Session, tenant, order, status, before string, limit int64) ([]Tickets, error) {
	rows := []Tickets{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and ($2='' or order_id::text=$2) and ($3='' or status=$3) and ($4='' or ticket_no<$4) order by ticket_no desc limit $5", ticketsRows, m.table), tenant, order, status, before, limit)
	return rows, err
}
func (m *customTicketsModel) UpdateStateTx(ctx context.Context, tx sqlx.Session, row *Tickets) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$3,assignee_user_id=$4,version=version+1,updated_at=now() where tenant_id=$1 and id=$2 and version=$5", m.table), row.TenantId, row.Id, row.Status, row.AssigneeUserId, row.Version))
}
