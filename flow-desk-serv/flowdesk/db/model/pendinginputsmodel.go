package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type PendingInputsModel interface {
	pendingInputsModel
	withSession(sqlx.Session) PendingInputsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *PendingInputs) error
	FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*PendingInputs, error)
	ListPendingByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]PendingInputs, error)
	UpdateStatusTx(ctx context.Context, tx sqlx.Session, row *PendingInputs) error
}
type customPendingInputsModel struct{ *defaultPendingInputsModel }

func NewPendingInputsModel(conn sqlx.SqlConn) PendingInputsModel {
	return &customPendingInputsModel{newPendingInputsModel(conn)}
}
func (m *customPendingInputsModel) withSession(tx sqlx.Session) PendingInputsModel {
	return NewPendingInputsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customPendingInputsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *PendingInputs) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customPendingInputsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*PendingInputs, error) {
	var row PendingInputs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", pendingInputsRows, m.table), tenant, id)
	return &row, err
}
func (m *customPendingInputsModel) ListPendingByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]PendingInputs, error) {
	rows := []PendingInputs{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 and status='pending' order by created_at,id", pendingInputsRows, m.table), tenant, run)
	return rows, err
}
func (m *customPendingInputsModel) UpdateStatusTx(ctx context.Context, tx sqlx.Session, row *PendingInputs) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$3,answer=$4,version=version+1 where tenant_id=$1 and id=$2 and version=$5", m.table), row.TenantId, row.Id, row.Status, row.Answer, row.Version))
}
