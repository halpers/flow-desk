package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ SourceObjectArchivesModel = (*customSourceObjectArchivesModel)(nil)

type (
	// SourceObjectArchivesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customSourceObjectArchivesModel.
	SourceObjectArchivesModel interface {
		InsertTx(context.Context, sqlx.Session, *SourceObjectArchives) error
		FindOneByTenantIdIdTx(context.Context, sqlx.Session, string, string) (*SourceObjectArchives, error)
		sourceObjectArchivesModel
		withSession(session sqlx.Session) SourceObjectArchivesModel
	}

	customSourceObjectArchivesModel struct {
		*defaultSourceObjectArchivesModel
	}
)

// NewSourceObjectArchivesModel returns a model for the database table.
func NewSourceObjectArchivesModel(conn sqlx.SqlConn) SourceObjectArchivesModel {
	return &customSourceObjectArchivesModel{
		defaultSourceObjectArchivesModel: newSourceObjectArchivesModel(conn),
	}
}

func (m *customSourceObjectArchivesModel) withSession(session sqlx.Session) SourceObjectArchivesModel {
	return NewSourceObjectArchivesModel(sqlx.NewSqlConnFromSession(session))
}

func (m *customSourceObjectArchivesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *SourceObjectArchives) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`insert into %s(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size,status,created_at,retired_at,archived_by)
    values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, m.table), row.Id, row.TenantId, row.VersionId, row.SourceEpoch, row.StoreId, row.ObjectKey, row.ContentHash, row.SourceSize, row.Status, row.CreatedAt, row.RetiredAt, row.ArchivedBy)
	return err
}
func (m *customSourceObjectArchivesModel) FindOneByTenantIdIdTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*SourceObjectArchives, error) {
	return m.withSession(tx).FindOneByTenantIdId(ctx, tenant, id)
}
