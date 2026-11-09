package model

import (
	"context"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type RunEventPayloadArchivesModel interface {
	runEventPayloadArchivesModel
	withSession(sqlx.Session) RunEventPayloadArchivesModel
	InsertRunTx(context.Context, sqlx.Session, string, string) error
	DeleteRestoredTx(context.Context, sqlx.Session, string, string) error
}
type customRunEventPayloadArchivesModel struct {
	*defaultRunEventPayloadArchivesModel
}

func NewRunEventPayloadArchivesModel(conn sqlx.SqlConn) RunEventPayloadArchivesModel {
	return &customRunEventPayloadArchivesModel{newRunEventPayloadArchivesModel(conn)}
}
func (m *customRunEventPayloadArchivesModel) withSession(tx sqlx.Session) RunEventPayloadArchivesModel {
	return NewRunEventPayloadArchivesModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customRunEventPayloadArchivesModel) InsertRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "insert into business.run_event_payload_archives(id,tenant_id,run_id,payload) select id,tenant_id,run_id,payload from business.run_events where tenant_id=$1 and run_id=$2", tenant, run)
	return err
}
func (m *customRunEventPayloadArchivesModel) DeleteRestoredTx(ctx context.Context, tx sqlx.Session, tenant, run string) error {
	_, err := tx.ExecCtx(ctx, "delete from business.run_event_payload_archives where tenant_id=$1 and run_id=$2", tenant, run)
	return err
}
