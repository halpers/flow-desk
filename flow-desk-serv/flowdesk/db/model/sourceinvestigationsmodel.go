package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type SourceInvestigationsModel interface {
	sourceInvestigationsModel
	withSession(sqlx.Session) SourceInvestigationsModel
	LockObjectTx(context.Context, sqlx.Session, string, string) error
	FindOneByTenantIdObjectRefTx(context.Context, sqlx.Session, string, string) (*SourceInvestigations, error)
	InsertTx(context.Context, sqlx.Session, *SourceInvestigations) error
	UpdateNoteTx(context.Context, sqlx.Session, *SourceInvestigations, string, string, string) error
}
type customSourceInvestigationsModel struct {
	*defaultSourceInvestigationsModel
}

func NewSourceInvestigationsModel(conn sqlx.SqlConn) SourceInvestigationsModel {
	return &customSourceInvestigationsModel{newSourceInvestigationsModel(conn)}
}
func (m *customSourceInvestigationsModel) withSession(tx sqlx.Session) SourceInvestigationsModel {
	return NewSourceInvestigationsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customSourceInvestigationsModel) LockObjectTx(ctx context.Context, tx sqlx.Session, tenant, ref string) error {
	_, err := tx.ExecCtx(ctx, "select pg_advisory_xact_lock(hashtextextended($1,15))", tenant+":"+ref)
	return err
}
func (m *customSourceInvestigationsModel) FindOneByTenantIdObjectRefTx(ctx context.Context, tx sqlx.Session, tenant, ref string) (*SourceInvestigations, error) {
	return m.withSession(tx).FindOneByTenantIdObjectRef(ctx, tenant, ref)
}
func (m *customSourceInvestigationsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *SourceInvestigations) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customSourceInvestigationsModel) UpdateNoteTx(ctx context.Context, tx sqlx.Session, row *SourceInvestigations, status, note, actor string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$4,note=$5,updated_by=$6,revision=revision+1,updated_at=now() where tenant_id=$1 and object_ref=$2 and revision=$3", m.table), row.TenantId, row.ObjectRef, row.Revision, status, note, actor))
}
