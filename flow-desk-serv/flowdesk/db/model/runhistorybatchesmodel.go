package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"time"
)

type RunHistoryBatchesModel interface {
	runHistoryBatchesModel
	FindOneTx(context.Context, sqlx.Session, string, string) (*RunHistoryBatches, error)
	InsertTx(context.Context, sqlx.Session, *RunHistoryBatches) error
	ListPageTx(context.Context, sqlx.Session, string, time.Time, string, int64) ([]RunHistoryBatches, error)
}
type customRunHistoryBatchesModel struct{ *defaultRunHistoryBatchesModel }

func NewRunHistoryBatchesModel(conn sqlx.SqlConn) RunHistoryBatchesModel {
	return &customRunHistoryBatchesModel{newRunHistoryBatchesModel(conn)}
}
func (m *customRunHistoryBatchesModel) FindOneTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*RunHistoryBatches, error) {
	var row RunHistoryBatches
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2", runHistoryBatchesRows, m.table), tenant, id)
	return &row, err
}
func (m *customRunHistoryBatchesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *RunHistoryBatches) error {
	_, err := newRunHistoryBatchesModel(sqlx.NewSqlConnFromSession(tx)).Insert(ctx, row)
	return err
}
func (m *customRunHistoryBatchesModel) ListPageTx(ctx context.Context, tx sqlx.Session, tenant string, before time.Time, beforeID string, limit int64) ([]RunHistoryBatches, error) {
	rows := []RunHistoryBatches{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and (created_at,id)<($2::timestamptz,$3::uuid) order by created_at desc,id desc limit $4", runHistoryBatchesRows, m.table), tenant, before, beforeID, limit)
	return rows, err
}
