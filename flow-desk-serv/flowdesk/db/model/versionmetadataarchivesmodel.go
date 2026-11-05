package model

import (
	"context"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ VersionMetadataArchivesModel = (*customVersionMetadataArchivesModel)(nil)

type (
	// VersionMetadataArchivesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customVersionMetadataArchivesModel.
	VersionMetadataArchivesModel interface {
		InsertTx(context.Context, sqlx.Session, *VersionMetadataArchives) error
		FindOneByTenantIdIdTx(context.Context, sqlx.Session, string, string) (*VersionMetadataArchives, error)
		versionMetadataArchivesModel
		withSession(session sqlx.Session) VersionMetadataArchivesModel
	}

	customVersionMetadataArchivesModel struct {
		*defaultVersionMetadataArchivesModel
	}
)

// NewVersionMetadataArchivesModel returns a model for the database table.
func NewVersionMetadataArchivesModel(conn sqlx.SqlConn) VersionMetadataArchivesModel {
	return &customVersionMetadataArchivesModel{
		defaultVersionMetadataArchivesModel: newVersionMetadataArchivesModel(conn),
	}
}

func (m *customVersionMetadataArchivesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *VersionMetadataArchives) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customVersionMetadataArchivesModel) FindOneByTenantIdIdTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*VersionMetadataArchives, error) {
	return m.withSession(tx).FindOneByTenantIdId(ctx, tenant, id)
}

func (m *customVersionMetadataArchivesModel) withSession(session sqlx.Session) VersionMetadataArchivesModel {
	return NewVersionMetadataArchivesModel(sqlx.NewSqlConnFromSession(session))
}
