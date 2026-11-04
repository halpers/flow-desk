package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type OutboxEventsModel interface {
	outboxEventsModel
	withSession(sqlx.Session) OutboxEventsModel

	InsertTx(context.Context, sqlx.Session, *OutboxEvents) error
	FindClaimableForUpdateTx(context.Context, sqlx.Session) (*OutboxEvents, error)
	UpdateClaimTx(context.Context, sqlx.Session, string) error
	UpdateDelivered(context.Context, string, int64) error
	UpdateRetry(context.Context, string, int64, string) error
	FindGraphVersionTx(context.Context, sqlx.Session, string, string) (string, error)
}
type customOutboxEventsModel struct{ *defaultOutboxEventsModel }

func NewOutboxEventsModel(conn sqlx.SqlConn) OutboxEventsModel {
	return &customOutboxEventsModel{newOutboxEventsModel(conn)}
}
func (m *customOutboxEventsModel) withSession(tx sqlx.Session) OutboxEventsModel {
	return NewOutboxEventsModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customOutboxEventsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *OutboxEvents) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customOutboxEventsModel) FindGraphVersionTx(ctx context.Context, tx sqlx.Session, tenant, run string) (string, error) {
	var row struct {
		GraphVersion string `db:"graph_version"`
	}
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select coalesce(payload->>'graph_version','read-v1') as graph_version from %s where tenant_id=$1 and aggregate_id=$2 and topic='run.execute' order by created_at,id limit 1", m.table), tenant, run)
	return row.GraphVersion, err
}
func (m *customOutboxEventsModel) FindClaimableForUpdateTx(ctx context.Context, tx sqlx.Session) (*OutboxEvents, error) {
	var row OutboxEvents
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where (status='pending' and available_at<=now()) or (status='delivering' and lease_expires_at<now()) order by available_at,id limit 1 for update skip locked", outboxEventsRows, m.table))
	return &row, err
}
func (m *customOutboxEventsModel) UpdateClaimTx(ctx context.Context, tx sqlx.Session, id string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status='delivering',attempt_count=attempt_count+1,lease_expires_at=now()+interval '30 seconds' where id=$1", m.table), id))
}
func (m *customOutboxEventsModel) UpdateDelivered(ctx context.Context, id string, attempt int64) error {
	return requireOne(m.conn.ExecCtx(ctx, fmt.Sprintf("update %s set status='delivered',lease_expires_at=null where id=$1 and status='delivering' and attempt_count=$2", m.table), id, attempt))
}
func (m *customOutboxEventsModel) UpdateRetry(ctx context.Context, id string, attempt int64, code string) error {
	return requireOne(m.conn.ExecCtx(ctx, fmt.Sprintf("update %s set status=case when attempt_count>=8 then 'dead_letter' else 'pending' end,available_at=now()+interval '1 second'*least(30,power(2,attempt_count)),last_error_code=$3,lease_expires_at=null where id=$1 and status='delivering' and attempt_count=$2", m.table), id, attempt, code))
}
