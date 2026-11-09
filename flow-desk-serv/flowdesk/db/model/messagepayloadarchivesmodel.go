package model

import (
	"context"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type MessagePayloadArchivesModel interface {
	messagePayloadArchivesModel
	withSession(sqlx.Session) MessagePayloadArchivesModel
	InsertRunTx(context.Context, sqlx.Session, string, string) error
	DeleteRestoredTx(context.Context, sqlx.Session, string, string) error
}
type customMessagePayloadArchivesModel struct {
	*defaultMessagePayloadArchivesModel
}

func NewMessagePayloadArchivesModel(conn sqlx.SqlConn) MessagePayloadArchivesModel {
	return &customMessagePayloadArchivesModel{newMessagePayloadArchivesModel(conn)}
}
func (m *customMessagePayloadArchivesModel) withSession(tx sqlx.Session) MessagePayloadArchivesModel {
	return NewMessagePayloadArchivesModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customMessagePayloadArchivesModel) InsertRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "insert into business.message_payload_archives(id,tenant_id,run_id,content,citations,action_receipts) select id,tenant_id,run_id,content,citations,action_receipts from business.messages where tenant_id=$1 and run_id=$2", tenant, run)
	return err
}
func (m *customMessagePayloadArchivesModel) DeleteRestoredTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "delete from business.message_payload_archives where tenant_id=$1 and run_id=$2", tenant, run)
	return err
}
