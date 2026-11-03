package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type OrdersModel interface {
	ordersModel
	withSession(sqlx.Session) OrdersModel

	FindOneByTenant(context.Context, string, string) (*Orders, error)
	FindOneByOrderNoTx(context.Context, sqlx.Session, string, string) (*Orders, error)
	FindOneForShareTx(context.Context, sqlx.Session, string, string) (*Orders, error)
	ListByTenant(context.Context, string, string, string, int64) ([]Orders, error)
}
type customOrdersModel struct{ *defaultOrdersModel }

func NewOrdersModel(conn sqlx.SqlConn) OrdersModel { return &customOrdersModel{newOrdersModel(conn)} }
func (m *customOrdersModel) withSession(tx sqlx.Session) OrdersModel {
	return NewOrdersModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customOrdersModel) FindOneByTenant(ctx context.Context, tenant, id string) (*Orders, error) {
	var row Orders
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2", ordersRows, m.table), tenant, id)
	return &row, err
}
func (m *customOrdersModel) FindOneByOrderNoTx(ctx context.Context, tx sqlx.Session, tenant, number string) (*Orders, error) {
	var row Orders
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and order_no=$2", ordersRows, m.table), tenant, number)
	return &row, err
}
func (m *customOrdersModel) ListByTenant(ctx context.Context, tenant, filter, after string, limit int64) ([]Orders, error) {
	rows := []Orders{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and ($2='' or order_no=$2) and order_no>$3 order by order_no limit $4", ordersRows, m.table), tenant, filter, after, limit)
	return rows, err
}
func (m *customOrdersModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Orders, error) {
	var row Orders
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", ordersRows, m.table), tenant, id)
	return &row, err
}
