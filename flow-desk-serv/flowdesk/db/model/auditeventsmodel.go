package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type AuditEventsModel interface {
	auditEventsModel
	withSession(sqlx.Session) AuditEventsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *AuditEvents) error
	ListTenantTx(ctx context.Context, tx sqlx.Session, tenant, before string, limit int64) ([]AuditEvents, error)
}
type customAuditEventsModel struct{ *defaultAuditEventsModel }

func NewAuditEventsModel(conn sqlx.SqlConn) AuditEventsModel {
	return &customAuditEventsModel{newAuditEventsModel(conn)}
}
func (m *customAuditEventsModel) withSession(tx sqlx.Session) AuditEventsModel {
	return NewAuditEventsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customAuditEventsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *AuditEvents) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customAuditEventsModel) ListTenantTx(ctx context.Context, tx sqlx.Session, tenant, before string, limit int64) ([]AuditEvents, error) {
	rows := []AuditEvents{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and ($2='' or id::text<$2) order by id desc limit $3", auditEventsRows, m.table), tenant, before, limit)
	return rows, err
}
