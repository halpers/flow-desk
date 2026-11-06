package model

import (
	"context"
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type SourceInvestigationsModel interface {
	sourceInvestigationsModel
	withSession(sqlx.Session) SourceInvestigationsModel
	FindOneForShareTx(context.Context, sqlx.Session, string, string) (*SourceInvestigations, error)
	LockObjectTx(context.Context, sqlx.Session, string, string) error
	FindOneByTenantIdObjectRefTx(context.Context, sqlx.Session, string, string) (*SourceInvestigations, error)
	InsertTx(context.Context, sqlx.Session, *SourceInvestigations) error
	UpdateNoteTx(context.Context, sqlx.Session, *SourceInvestigations, string, string, string) error
	ListPageTx(context.Context, sqlx.Session, string, string, string, string, int64) ([]SourceInvestigationListRow, error)
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

// Only the private key needed for current authorization is projected; notes stay out of list reads.
type SourceInvestigationListRow struct {
	Id        string    `db:"id"`
	ObjectRef string    `db:"object_ref"`
	ObjectKey string    `db:"object_key"`
	Revision  int64     `db:"revision"`
	Status    string    `db:"status"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedBy string    `db:"updated_by"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (m *customSourceInvestigationsModel) ListPageTx(ctx context.Context, tx sqlx.Session, tenant, status, before, beforeID string, limit int64) ([]SourceInvestigationListRow, error) {
	rows := []SourceInvestigationListRow{}
	query := fmt.Sprintf("select id,object_ref,object_key,revision,status,created_at,updated_by,updated_at from %s where tenant_id=$1", m.table)
	args := []any{tenant}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" and status=$%d", len(args))
	}
	if before != "" {
		args = append(args, before, beforeID)
		query += fmt.Sprintf(" and (created_at,id)<($%d::timestamptz,$%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" order by created_at desc,id desc limit $%d", len(args))
	err := tx.QueryRowsCtx(ctx, &rows, query, args...)
	return rows, err
}

func (m *customSourceInvestigationsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, ref string) (*SourceInvestigations, error) {
	var row SourceInvestigations
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and object_ref=$2 for share", sourceInvestigationsRows, m.table), tenant, ref)
	return &row, err
}
