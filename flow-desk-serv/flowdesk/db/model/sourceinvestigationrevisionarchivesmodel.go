package model

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const investigationRevisionArchivesTable = `"business"."source_investigation_revision_archives"`

type SourceInvestigationRevisionArchivesModel interface {
	sourceInvestigationRevisionArchivesModel
	withSession(sqlx.Session) SourceInvestigationRevisionArchivesModel
	InsertSnapshotTx(context.Context, sqlx.Session, *SourceInvestigationRevisions, string) error
	FindOneRevisionTx(context.Context, sqlx.Session, string, string, int64) (*SourceInvestigationRevisionArchives, error)
	ListRevisionsTx(context.Context, sqlx.Session, string, string, []int64) ([]SourceInvestigationRevisionArchives, error)
}
type customSourceInvestigationRevisionArchivesModel struct {
	*defaultSourceInvestigationRevisionArchivesModel
}

func NewSourceInvestigationRevisionArchivesModel(conn sqlx.SqlConn) SourceInvestigationRevisionArchivesModel {
	return &customSourceInvestigationRevisionArchivesModel{newSourceInvestigationRevisionArchivesModel(conn)}
}
func (m *customSourceInvestigationRevisionArchivesModel) withSession(tx sqlx.Session) SourceInvestigationRevisionArchivesModel {
	return NewSourceInvestigationRevisionArchivesModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customSourceInvestigationRevisionArchivesModel) InsertSnapshotTx(ctx context.Context, tx sqlx.Session, row *SourceInvestigationRevisions, actor string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`insert into %s(id,tenant_id,investigation_id,revision,status,note,changed_by,changed_at,is_baseline,archived_by)
    values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, m.table), row.Id, row.TenantId, row.InvestigationId, row.Revision, row.Status, row.Note, row.ChangedBy, row.ChangedAt, row.IsBaseline, actor)
	return err
}
func (m *customSourceInvestigationRevisionArchivesModel) FindOneRevisionTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, revision int64) (*SourceInvestigationRevisionArchives, error) {
	var row SourceInvestigationRevisionArchives
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and investigation_id=$2 and revision=$3", sourceInvestigationRevisionArchivesRows, m.table), tenant, investigation, revision)
	return &row, err
}
func (m *customSourceInvestigationRevisionArchivesModel) ListRevisionsTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, revisions []int64) ([]SourceInvestigationRevisionArchives, error) {
	rows := []SourceInvestigationRevisionArchives{}
	if len(revisions) == 0 {
		return rows, nil
	}
	args := []any{tenant, investigation}
	placeholders := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		args = append(args, revision)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and investigation_id=$2 and revision in (%s)", sourceInvestigationRevisionArchivesRows, m.table, strings.Join(placeholders, ",")), args...)
	return rows, err
}
