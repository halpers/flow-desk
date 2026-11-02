package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"time"
)

type ConversationsModel interface {
	conversationsModel
	withSession(sqlx.Session) ConversationsModel

	InsertTx(context.Context, sqlx.Session, *Conversations) error
	FindOneOwned(context.Context, string, string, string) (*Conversations, error)
	FindOneOwnedForUpdateTx(context.Context, sqlx.Session, string, string, string) (*Conversations, error)
	ListOwned(context.Context, string, string, time.Time, string, int64) ([]Conversations, error)
	UpdateMessageCountTx(context.Context, sqlx.Session, string, int64) error
}
type customConversationsModel struct{ *defaultConversationsModel }

func NewConversationsModel(conn sqlx.SqlConn) ConversationsModel {
	return &customConversationsModel{newConversationsModel(conn)}
}
func (m *customConversationsModel) withSession(tx sqlx.Session) ConversationsModel {
	return NewConversationsModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customConversationsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Conversations) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customConversationsModel) FindOneOwned(ctx context.Context, tenant, actor, id string) (*Conversations, error) {
	var row Conversations
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and owner_user_id=$2 and id=$3", conversationsRows, m.table), tenant, actor, id)
	return &row, err
}
func (m *customConversationsModel) FindOneOwnedForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, actor, id string) (*Conversations, error) {
	var row Conversations
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and owner_user_id=$2 and id=$3 for update", conversationsRows, m.table), tenant, actor, id)
	return &row, err
}
func (m *customConversationsModel) ListOwned(ctx context.Context, tenant, actor string, before time.Time, beforeID string, limit int64) ([]Conversations, error) {
	rows := []Conversations{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and owner_user_id=$2 and (created_at,id)<($3,$4) order by created_at desc,id desc limit $5", conversationsRows, m.table), tenant, actor, before, beforeID, limit)
	return rows, err
}
func (m *customConversationsModel) UpdateMessageCountTx(ctx context.Context, tx sqlx.Session, id string, previous int64) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set message_count=message_count+2,version=version+1,updated_at=now() where id=$1 and message_count=$2", m.table), id, previous))
}
