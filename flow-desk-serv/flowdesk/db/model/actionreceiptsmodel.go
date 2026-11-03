package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type ActionReceiptsModel interface {
	actionReceiptsModel
	withSession(sqlx.Session) ActionReceiptsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *ActionReceipts) error
	FindOneByActionTx(ctx context.Context, tx sqlx.Session, tenant, action string) (*ActionReceipts, error)
	ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]ActionReceipts, error)
}
type customActionReceiptsModel struct{ *defaultActionReceiptsModel }

func NewActionReceiptsModel(conn sqlx.SqlConn) ActionReceiptsModel {
	return &customActionReceiptsModel{newActionReceiptsModel(conn)}
}
func (m *customActionReceiptsModel) withSession(tx sqlx.Session) ActionReceiptsModel {
	return NewActionReceiptsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customActionReceiptsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *ActionReceipts) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customActionReceiptsModel) FindOneByActionTx(ctx context.Context, tx sqlx.Session, tenant, action string) (*ActionReceipts, error) {
	var row ActionReceipts
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and action_id=$2", actionReceiptsRows, m.table), tenant, action)
	return &row, err
}
func (m *customActionReceiptsModel) ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]ActionReceipts, error) {
	rows := []ActionReceipts{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select r.* from %s r join business.actions a on (a.tenant_id,a.id)=(r.tenant_id,r.action_id) where r.tenant_id=$1 and a.run_id=$2 order by r.executed_at,r.id", m.table), tenant, run)
	return rows, err
}
