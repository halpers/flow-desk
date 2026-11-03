package model

import (
	"context"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type TicketNotesModel interface {
	ticketNotesModel
	withSession(sqlx.Session) TicketNotesModel
	InsertTx(ctx context.Context, tx sqlx.Session, row *TicketNotes) error
}
type customTicketNotesModel struct{ *defaultTicketNotesModel }

func NewTicketNotesModel(conn sqlx.SqlConn) TicketNotesModel {
	return &customTicketNotesModel{newTicketNotesModel(conn)}
}
func (m *customTicketNotesModel) withSession(tx sqlx.Session) TicketNotesModel {
	return NewTicketNotesModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customTicketNotesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *TicketNotes) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
