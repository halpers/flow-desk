package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ DocumentsModel = (*customDocumentsModel)(nil)

type (
	// DocumentsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customDocumentsModel.
	DocumentsModel interface {
		FindOneForShareTx(context.Context, sqlx.Session, string, string) (*Documents, error)
		documentsModel
		withSession(session sqlx.Session) DocumentsModel
		InsertTx(context.Context, sqlx.Session, *Documents) error
		CountByBaseTx(context.Context, sqlx.Session, string, string) (int64, error)
		ListByBase(context.Context, string, string) ([]Documents, error)
		ListByBaseTx(context.Context, sqlx.Session, string, string) ([]Documents, error)
		FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*Documents, error)
		UpdateActiveVersionTx(context.Context, sqlx.Session, string, string, int64, string, string) error
	}

	customDocumentsModel struct {
		*defaultDocumentsModel
	}
)

// NewDocumentsModel returns a model for the database table.
func NewDocumentsModel(conn sqlx.SqlConn) DocumentsModel {
	return &customDocumentsModel{
		defaultDocumentsModel: newDocumentsModel(conn),
	}
}

func (m *customDocumentsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Documents, error) {
	var row Documents
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", documentsRows, m.table), tenant, id)
	return &row, err
}
func (m *customDocumentsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Documents) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customDocumentsModel) CountByBaseTx(ctx context.Context, tx sqlx.Session, tenant, base string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1 and knowledge_base_id=$2", m.table), tenant, base)
	return count, err
}
func (m *customDocumentsModel) ListByBase(ctx context.Context, tenant, base string) ([]Documents, error) {
	rows := []Documents{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and knowledge_base_id=$2 order by created_at,id limit 201", documentsRows, m.table), tenant, base)
	return rows, err
}
func (m *customDocumentsModel) ListByBaseTx(ctx context.Context, tx sqlx.Session, tenant, base string) ([]Documents, error) {
	rows := []Documents{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and knowledge_base_id=$2 order by created_at,id limit 201", documentsRows, m.table), tenant, base)
	return rows, err
}
func (m *customDocumentsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Documents, error) {
	var row Documents
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", documentsRows, m.table), tenant, id)
	return &row, err
}
func (m *customDocumentsModel) UpdateActiveVersionTx(ctx context.Context, tx sqlx.Session, tenant, id string, version int64, active, status string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set active_version_id=nullif($4,'')::uuid,status=$5,version=version+1,updated_at=now() where tenant_id=$1 and id=$2 and version=$3", m.table), tenant, id, version, active, status))
}

func (m *customDocumentsModel) withSession(session sqlx.Session) DocumentsModel {
	return NewDocumentsModel(sqlx.NewSqlConnFromSession(session))
}
