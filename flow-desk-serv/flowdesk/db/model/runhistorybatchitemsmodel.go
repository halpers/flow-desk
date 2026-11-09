package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"strings"
)

type RunHistoryBatchItemsModel interface {
	runHistoryBatchItemsModel
	InsertTx(context.Context, sqlx.Session, *RunHistoryBatchItems) error
	ListOperationsTx(context.Context, sqlx.Session, string, string) ([]RunHistoryOperations, error)
}
type customRunHistoryBatchItemsModel struct {
	*defaultRunHistoryBatchItemsModel
}

func NewRunHistoryBatchItemsModel(conn sqlx.SqlConn) RunHistoryBatchItemsModel {
	return &customRunHistoryBatchItemsModel{newRunHistoryBatchItemsModel(conn)}
}
func (m *customRunHistoryBatchItemsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *RunHistoryBatchItems) error {
	_, err := newRunHistoryBatchItemsModel(sqlx.NewSqlConnFromSession(tx)).Insert(ctx, row)
	return err
}
func (m *customRunHistoryBatchItemsModel) ListOperationsTx(ctx context.Context, tx sqlx.Session, tenant, batch string) ([]RunHistoryOperations, error) {
	rows := []RunHistoryOperations{}
	fields := "o." + strings.ReplaceAll(runHistoryOperationsRows, ",", ",o.")
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s i join business.run_history_operations o on o.tenant_id=i.tenant_id and o.id=i.operation_id where i.tenant_id=$1 and i.batch_id=$2 order by i.ordinal", fields, m.table), tenant, batch)
	return rows, err
}
