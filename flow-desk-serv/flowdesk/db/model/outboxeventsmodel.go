package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type OutboxEventsModel interface {
	FindOneOwnedForUpdateTx(context.Context, sqlx.Session, string, int64) (*OutboxEvents, error)
	UpsertArchiveTx(context.Context, sqlx.Session, *OutboxEvents) error
	outboxEventsModel
	withSession(sqlx.Session) OutboxEventsModel

	InsertTx(context.Context, sqlx.Session, *OutboxEvents) error
	FindClaimableForUpdateTx(context.Context, sqlx.Session) (*OutboxEvents, error)
	UpdateClaimTx(context.Context, sqlx.Session, string) error
	UpdateDelivered(context.Context, string, int64) error
	UpdateRetry(context.Context, string, int64, string) error
	FindGraphVersionTx(context.Context, sqlx.Session, string, string) (string, error)
}

func (m *customOutboxEventsModel) FindOneOwnedForUpdateTx(ctx context.Context, tx sqlx.Session, id string, attempt int64) (*OutboxEvents, error) {
	var row OutboxEvents
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where id=$1 and attempt_count=$2 and status='delivering' and lease_expires_at>now() for update", outboxEventsRows, m.table), id, attempt)
	return &row, err
}
func (m *customOutboxEventsModel) UpsertArchiveTx(ctx context.Context, tx sqlx.Session, row *OutboxEvents) error {
	// Never reset the fencing generation when manually requeuing dead letters.
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("insert into %s(id,tenant_id,aggregate_id,dispatch_id,topic,payload,status) values($1,$2,$3,$4,'knowledge.archive',$5,'pending') on conflict(dispatch_id) do update set status='pending',available_at=now(),last_error_code='',lease_expires_at=null where outbox_events.status='dead_letter' and outbox_events.topic='knowledge.archive'", m.table), row.Id, row.TenantId, row.AggregateId, row.DispatchId, row.Payload)
	return err
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
	return requireOne(m.conn.ExecCtx(ctx, fmt.Sprintf("update %s set status=case when topic='knowledge.cleanup' then 'pending' when attempt_count%%8=0 then 'dead_letter' else 'pending' end,available_at=now()+interval '1 second'*least(30,power(2,least(attempt_count,5))),last_error_code=$3,lease_expires_at=null where id=$1 and status='delivering' and attempt_count=$2", m.table), id, attempt, code))
}
