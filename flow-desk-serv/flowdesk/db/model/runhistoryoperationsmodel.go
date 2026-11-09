package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type RunHistoryOperationsModel interface {
	runHistoryOperationsModel
	withSession(sqlx.Session) RunHistoryOperationsModel
	LockMaintenanceTx(context.Context, sqlx.Session, string) error
	FindLatestTx(context.Context, sqlx.Session, string, string) (*RunHistoryOperations, error)
	FindOneTx(context.Context, sqlx.Session, string, string, string) (*RunHistoryOperations, error)
	InsertTx(context.Context, sqlx.Session, *RunHistoryOperations) error
	UpdateCompletedTx(context.Context, sqlx.Session, *RunHistoryOperations) error
}
type customRunHistoryOperationsModel struct {
	*defaultRunHistoryOperationsModel
}

func NewRunHistoryOperationsModel(conn sqlx.SqlConn) RunHistoryOperationsModel {
	return &customRunHistoryOperationsModel{newRunHistoryOperationsModel(conn)}
}
func (m *customRunHistoryOperationsModel) withSession(tx sqlx.Session) RunHistoryOperationsModel {
	return NewRunHistoryOperationsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customRunHistoryOperationsModel) FindLatestTx(ctx context.Context, tx sqlx.Session, tenant, run string) (*RunHistoryOperations, error) {
	var row RunHistoryOperations
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 order by generation desc limit 1", runHistoryOperationsRows, m.table), tenant, run)
	return &row, err
}
func (m *customRunHistoryOperationsModel) FindOneTx(ctx context.Context, tx sqlx.Session, tenant, run, id string) (*RunHistoryOperations, error) {
	var row RunHistoryOperations
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 and id=$3", runHistoryOperationsRows, m.table), tenant, run, id)
	return &row, err
}
func (m *customRunHistoryOperationsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *RunHistoryOperations) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customRunHistoryOperationsModel) UpdateCompletedTx(ctx context.Context, tx sqlx.Session, row *RunHistoryOperations) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status='completed',completed_at=now(),checkpoint_count=$4,checkpoint_blob_count=$5,checkpoint_write_count=$6 where tenant_id=$1 and run_id=$2 and id=$3 and status='pending'", m.table), row.TenantId, row.RunId, row.Id, row.CheckpointCount, row.CheckpointBlobCount, row.CheckpointWriteCount))
}

func (m *customRunHistoryOperationsModel) LockMaintenanceTx(ctx context.Context, tx sqlx.Session, id string) error {
	_, err := tx.ExecCtx(ctx, "select set_config('flowdesk.history_operation',$1,true)", id)
	return err
}
