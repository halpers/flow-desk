package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type RunEventsModel interface {
	UpdateArchivedPayloadTx(context.Context, sqlx.Session, string, string) error
	UpdateRestoredPayloadTx(context.Context, sqlx.Session, string, string) error
	runEventsModel
	withSession(sqlx.Session) RunEventsModel

	InsertTx(context.Context, sqlx.Session, *RunEvents) error
	FindOneBySourceTx(context.Context, sqlx.Session, string, string, string) (*RunEvents, error)
	ListAfterSequence(context.Context, string, string, int64, int64) ([]RunEvents, error)
}
type customRunEventsModel struct{ *defaultRunEventsModel }

func NewRunEventsModel(conn sqlx.SqlConn) RunEventsModel {
	return &customRunEventsModel{newRunEventsModel(conn)}
}
func (m *customRunEventsModel) withSession(tx sqlx.Session) RunEventsModel {
	return NewRunEventsModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customRunEventsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *RunEvents) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customRunEventsModel) FindOneBySourceTx(ctx context.Context, tx sqlx.Session, tenant, run, source string) (*RunEvents, error) {
	var row RunEvents
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 and source_event_id=$3", runEventsRows, `"business"."run_events_history_read"`), tenant, run, source)
	return &row, err
}
func (m *customRunEventsModel) ListAfterSequence(ctx context.Context, tenant, run string, after, limit int64) ([]RunEvents, error) {
	rows := []RunEvents{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 and seq>$3 order by seq limit $4", runEventsRows, `"business"."run_events_history_read"`), tenant, run, after, limit)
	return rows, err
}

func (m *customRunEventsModel) UpdateArchivedPayloadTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "update business.run_events set payload='{}' where tenant_id=$1 and run_id=$2", tenant, run)
	return err
}
func (m *customRunEventsModel) UpdateRestoredPayloadTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "update business.run_events x set payload=a.payload from business.run_event_payload_archives a where x.id=a.id and x.tenant_id=a.tenant_id and x.tenant_id=$1 and x.run_id=$2", tenant, run)
	return err
}
