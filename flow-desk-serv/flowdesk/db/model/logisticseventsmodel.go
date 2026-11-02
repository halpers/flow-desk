package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type LogisticsEventsModel interface {
	logisticsEventsModel
	withSession(sqlx.Session) LogisticsEventsModel

	ListByOrder(context.Context, string, string) ([]LogisticsEvents, error)
	ListByOrderTx(context.Context, sqlx.Session, string, string) ([]LogisticsEvents, error)
}
type customLogisticsEventsModel struct{ *defaultLogisticsEventsModel }

func NewLogisticsEventsModel(conn sqlx.SqlConn) LogisticsEventsModel {
	return &customLogisticsEventsModel{newLogisticsEventsModel(conn)}
}
func (m *customLogisticsEventsModel) withSession(tx sqlx.Session) LogisticsEventsModel {
	return NewLogisticsEventsModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customLogisticsEventsModel) ListByOrder(ctx context.Context, tenant, order string) ([]LogisticsEvents, error) {
	rows := []LogisticsEvents{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and order_id=$2 order by occurred_at,id limit 100", logisticsEventsRows, m.table), tenant, order)
	return rows, err
}
func (m *customLogisticsEventsModel) ListByOrderTx(ctx context.Context, tx sqlx.Session, tenant, order string) ([]LogisticsEvents, error) {
	rows := []LogisticsEvents{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and order_id=$2 order by occurred_at,id limit 100", logisticsEventsRows, m.table), tenant, order)
	return rows, err
}
