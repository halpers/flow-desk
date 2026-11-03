package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type MessagesModel interface {
	FindOneByClientMessageTx(context.Context, sqlx.Session, string, string, string) (*Messages, error)
	messagesModel
	withSession(sqlx.Session) MessagesModel

	InsertTx(context.Context, sqlx.Session, *Messages) error
	FindOneTx(context.Context, sqlx.Session, string, string) (*Messages, error)
	ListByConversation(context.Context, string, string, int64, int64) ([]Messages, error)
	UpdateFinalTx(context.Context, sqlx.Session, string, string, string, string) error
	UpdateAbortedTx(context.Context, sqlx.Session, string) error
	UpdateReceiptsTx(context.Context, sqlx.Session, string, string) error
	UpdateInputPositionTx(context.Context, sqlx.Session, string, int64, int64) error
}

func (m *customMessagesModel) FindOneByClientMessageTx(ctx context.Context, tx sqlx.Session, tenant, conversation, clientID string) (*Messages, error) {
	var row Messages
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and conversation_id=$2 and client_message_id=$3", messagesRows, m.table), tenant, conversation, clientID)
	return &row, err
}

type customMessagesModel struct{ *defaultMessagesModel }

func NewMessagesModel(conn sqlx.SqlConn) MessagesModel {
	return &customMessagesModel{newMessagesModel(conn)}
}
func (m *customMessagesModel) withSession(tx sqlx.Session) MessagesModel {
	return NewMessagesModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customMessagesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Messages) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customMessagesModel) FindOneTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Messages, error) {
	var row Messages
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2", messagesRows, m.table), tenant, id)
	return &row, err
}
func (m *customMessagesModel) ListByConversation(ctx context.Context, tenant, conversation string, after, limit int64) ([]Messages, error) {
	rows := []Messages{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and conversation_id=$2 and position>$3 order by position limit $4", messagesRows, m.table), tenant, conversation, after, limit)
	return rows, err
}
func (m *customMessagesModel) UpdateFinalTx(ctx context.Context, tx sqlx.Session, id, content, citations, receipts string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set content=$2,citations=$3,action_receipts=$4,status='final',updated_at=now() where id=$1 and status='streaming'", m.table), id, content, citations, receipts))
}
func (m *customMessagesModel) UpdateAbortedTx(ctx context.Context, tx sqlx.Session, id string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("update %s set status='aborted',updated_at=now() where id=$1 and status='streaming'", m.table), id)
	return err
}
func (m *customMessagesModel) UpdateReceiptsTx(ctx context.Context, tx sqlx.Session, id, receipts string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set action_receipts=$2,updated_at=now() where id=$1", m.table), id, receipts))
}
func (m *customMessagesModel) UpdateInputPositionTx(ctx context.Context, tx sqlx.Session, id string, previous, next int64) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set position=$3 where id=$1 and position=$2 and role='assistant' and status='streaming'", m.table), id, previous, next))
}
