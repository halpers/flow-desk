package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type RunTicketResourcesModel interface {
	runTicketResourcesModel
	InsertIfAbsentTx(context.Context, sqlx.Session, *RunTicketResources) error
	ListTicketIdsTx(context.Context, sqlx.Session, string, string) ([]TicketResource, error)
	ExistsInaccessibleTx(context.Context, sqlx.Session, string, string, string, string) (bool, error)
}
type TicketResource struct {
	TicketId string `db:"ticket_id"`
}
type customRunTicketResourcesModel struct {
	*defaultRunTicketResourcesModel
}

func NewRunTicketResourcesModel(conn sqlx.SqlConn) RunTicketResourcesModel {
	return &customRunTicketResourcesModel{newRunTicketResourcesModel(conn)}
}
func (m *customRunTicketResourcesModel) ExistsInaccessibleTx(ctx context.Context, tx sqlx.Session, tenant, run, actor, role string) (bool, error) {
	var denied bool
	err := tx.QueryRowCtx(ctx, &denied, fmt.Sprintf(`select exists(select 1 from %s r join business.tickets t on t.tenant_id=r.tenant_id and t.id=r.ticket_id where r.tenant_id=$1 and r.run_id=$2 and t.team_id is not null and $4<>'admin' and not exists(select 1 from business.team_members tm where tm.tenant_id=t.tenant_id and tm.team_id=t.team_id and tm.user_id=$3))`, m.table), tenant, run, actor, role)
	return denied, err
}
func (m *customRunTicketResourcesModel) InsertIfAbsentTx(ctx context.Context, tx sqlx.Session, row *RunTicketResources) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("insert into %s(id,tenant_id,run_id,ticket_id) values($1,$2,$3,$4) on conflict(tenant_id,run_id,ticket_id) do nothing", m.table), row.Id, row.TenantId, row.RunId, row.TicketId)
	return err
}
func (m *customRunTicketResourcesModel) ListTicketIdsTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]TicketResource, error) {
	rows := []TicketResource{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select ticket_id from %s where tenant_id=$1 and run_id=$2 order by ticket_id", m.table), tenant, run)
	return rows, err
}
