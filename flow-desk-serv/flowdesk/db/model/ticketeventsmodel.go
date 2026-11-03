package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type TicketEventsModel interface {
	ticketEventsModel
	withSession(sqlx.Session) TicketEventsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *TicketEvents) error
	ListByTicketTx(ctx context.Context, tx sqlx.Session, tenant, ticket, after string, limit int64) ([]TicketEvents, error)
}
type customTicketEventsModel struct{ *defaultTicketEventsModel }

func NewTicketEventsModel(conn sqlx.SqlConn) TicketEventsModel {
	return &customTicketEventsModel{newTicketEventsModel(conn)}
}
func (m *customTicketEventsModel) withSession(tx sqlx.Session) TicketEventsModel {
	return NewTicketEventsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customTicketEventsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *TicketEvents) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customTicketEventsModel) ListByTicketTx(ctx context.Context, tx sqlx.Session, tenant, ticket, after string, limit int64) ([]TicketEvents, error) {
	rows := []TicketEvents{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and ticket_id=$2 and id::text>$3 order by id limit $4", ticketEventsRows, m.table), tenant, ticket, after, limit)
	return rows, err
}
