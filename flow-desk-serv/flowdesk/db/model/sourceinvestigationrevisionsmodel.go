package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type SourceInvestigationRevisionsModel interface {
	sourceInvestigationRevisionsModel
	withSession(sqlx.Session) SourceInvestigationRevisionsModel
	ListPageTx(context.Context, sqlx.Session, string, string, int64, int64, int64) ([]SourceInvestigationRevisions, error)
	FindOneRevisionTx(context.Context, sqlx.Session, string, string, int64) (*SourceInvestigationRevisions, error)
	DeleteArchivedTx(context.Context, sqlx.Session, string, string, int64) error
	FindFirstRevisionTx(context.Context, sqlx.Session, string, string) (int64, error)
}
type customSourceInvestigationRevisionsModel struct {
	*defaultSourceInvestigationRevisionsModel
}

func NewSourceInvestigationRevisionsModel(conn sqlx.SqlConn) SourceInvestigationRevisionsModel {
	return &customSourceInvestigationRevisionsModel{newSourceInvestigationRevisionsModel(conn)}
}
func (m *customSourceInvestigationRevisionsModel) withSession(tx sqlx.Session) SourceInvestigationRevisionsModel {
	return NewSourceInvestigationRevisionsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customSourceInvestigationRevisionsModel) ListPageTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, upper, before, limit int64) ([]SourceInvestigationRevisions, error) {
	rows := []SourceInvestigationRevisions{}
	query := "select * from (" + m.historyQuery() + ") history where revision<=$3"
	args := []any{tenant, investigation, upper}
	if before > 0 {
		args = append(args, before)
		query += " and revision<$4"
	}
	args = append(args, limit)
	query += fmt.Sprintf(" order by revision desc limit $%d", len(args))
	err := tx.QueryRowsCtx(ctx, &rows, query, args...)
	return rows, err
}
func (m *customSourceInvestigationRevisionsModel) FindFirstRevisionTx(ctx context.Context, tx sqlx.Session, tenant, investigation string) (int64, error) {
	var revision int64
	err := tx.QueryRowCtx(ctx, &revision, "select revision from ("+m.historyQuery()+") history order by revision asc limit 1", tenant, investigation)
	return revision, err
}

func (m *customSourceInvestigationRevisionsModel) FindOneRevisionTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, revision int64) (*SourceInvestigationRevisions, error) {
	var row SourceInvestigationRevisions
	err := tx.QueryRowCtx(ctx, &row, "select * from ("+m.historyQuery()+") history where revision=$3", tenant, investigation, revision)
	return &row, err
}

func (m *customSourceInvestigationRevisionsModel) historyQuery() string {
	return fmt.Sprintf("select %s from %s where tenant_id=$1 and investigation_id=$2 union all select %s from %s where tenant_id=$1 and investigation_id=$2", sourceInvestigationRevisionsRows, m.table, sourceInvestigationRevisionsRows, investigationRevisionArchivesTable)
}
func (m *customSourceInvestigationRevisionsModel) DeleteArchivedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("delete from %s where tenant_id=$1 and id=$2 and revision=$3", m.table), tenant, id, revision))
}
