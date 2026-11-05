package model

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ UsersModel = (*customUsersModel)(nil)

type (
	// UsersModel is an interface to be customized, add more methods here,
	// and implement the added methods in customUsersModel.
	UsersModel interface {
		usersModel
		withSession(session sqlx.Session) UsersModel
		FindOneTx(context.Context, sqlx.Session, string) (*Users, error)
		SetEnabledTx(context.Context, sqlx.Session, string, bool) error
		ListTeamDirectory(context.Context, string) ([]TeamUser, error)
		FindOneForShareTx(context.Context, sqlx.Session, string, string) (*Users, error)
		FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*Users, error)
		ListManagedTx(context.Context, sqlx.Session, string, string, int64) ([]Users, error)
		CountManagedTx(context.Context, sqlx.Session, string) (int64, error)
		CountEnabledAdminsTx(context.Context, sqlx.Session, string) (int64, error)
		InsertTx(context.Context, sqlx.Session, *Users) error
		UpdateManagedTx(context.Context, sqlx.Session, *Users) error
	}

	customUsersModel struct {
		*defaultUsersModel
	}
)

func (m *customUsersModel) Insert(ctx context.Context, row *Users) (sql.Result, error) {
	if row.Revision == 0 {
		row.Revision = 1
	}
	if row.AuthVersion == 0 {
		row.AuthVersion = 1
	}
	if row.ExtraPermissions == "" {
		row.ExtraPermissions = "[]"
	}
	return m.defaultUsersModel.Insert(ctx, row)
}
func (m *customUsersModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Users) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customUsersModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Users, error) {
	var row Users
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", usersRows, m.table), tenant, id)
	return &row, err
}
func (m *customUsersModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Users, error) {
	var row Users
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", usersRows, m.table), tenant, id)
	return &row, err
}
func (m *customUsersModel) ListManagedTx(ctx context.Context, tx sqlx.Session, tenant, before string, limit int64) ([]Users, error) {
	rows := []Users{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and id::text>$2 order by id limit $3", usersRows, m.table), tenant, before, limit)
	return rows, err
}
func (m *customUsersModel) CountManagedTx(ctx context.Context, tx sqlx.Session, tenant string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1", m.table), tenant)
	return count, err
}
func (m *customUsersModel) CountEnabledAdminsTx(ctx context.Context, tx sqlx.Session, tenant string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1 and enabled and role='admin'", m.table), tenant)
	return count, err
}
func (m *customUsersModel) UpdateManagedTx(ctx context.Context, tx sqlx.Session, row *Users) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set display_name=$4,enabled=$5,role=$6,extra_permissions=$7, password_hash=$8,revision=revision+1,auth_version=auth_version+1 where tenant_id=$1 and id=$2 and revision=$3", m.table), row.TenantId, row.Id, row.Revision, row.DisplayName, row.Enabled, row.Role, row.ExtraPermissions, row.PasswordHash))
}

// NewUsersModel returns a model for the database table.
func NewUsersModel(conn sqlx.SqlConn) UsersModel {
	return &customUsersModel{
		defaultUsersModel: newUsersModel(conn),
	}
}

func (m *customUsersModel) ListTeamDirectory(ctx context.Context, tenant string) ([]TeamUser, error) {
	rows := []TeamUser{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select id,display_name,role,enabled from %s where tenant_id=$1 order by display_name,id limit 501", m.table), tenant)
	return rows, err
}
func (m *customUsersModel) SetEnabledTx(ctx context.Context, tx sqlx.Session, id string, enabled bool) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set enabled=$2 where id=$1", m.table), id, enabled))
}

func (m *customUsersModel) FindOneTx(ctx context.Context, tx sqlx.Session, id string) (*Users, error) {
	return m.withSession(tx).FindOne(ctx, id)
}

func (m *customUsersModel) withSession(session sqlx.Session) UsersModel {
	return NewUsersModel(sqlx.NewSqlConnFromSession(session))
}
