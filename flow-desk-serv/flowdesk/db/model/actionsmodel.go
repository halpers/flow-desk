package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type ActionsModel interface {
	actionsModel
	withSession(sqlx.Session) ActionsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *Actions) error
	FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Actions, error)
	ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]Actions, error)
	UpdateStatusTx(ctx context.Context, tx sqlx.Session, tenant, id, status string) error
}
type customActionsModel struct{ *defaultActionsModel }

func NewActionsModel(conn sqlx.SqlConn) ActionsModel {
	return &customActionsModel{newActionsModel(conn)}
}
func (m *customActionsModel) withSession(tx sqlx.Session) ActionsModel {
	return NewActionsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customActionsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Actions) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customActionsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Actions, error) {
	var row Actions
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", actionsRows, m.table), tenant, id)
	return &row, err
}
func (m *customActionsModel) ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]Actions, error) {
	rows := []Actions{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 order by created_at,id", actionsRows, m.table), tenant, run)
	return rows, err
}
func (m *customActionsModel) UpdateStatusTx(ctx context.Context, tx sqlx.Session, tenant, id, status string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$3,version=version+1 where tenant_id=$1 and id=$2", m.table), tenant, id, status))
}
