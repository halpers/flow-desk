package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ UserRolesModel = (*customUserRolesModel)(nil)

type (
	// UserRolesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customUserRolesModel.
	UserRolesModel interface {
		userRolesModel
		withSession(session sqlx.Session) UserRolesModel
		ListRoleIDsTx(context.Context, sqlx.Session, string, string) ([]string, error)
		ListUserIDsTx(context.Context, sqlx.Session, string, string) ([]string, error)
		DeleteByUserTx(context.Context, sqlx.Session, string, string) error
		InsertTx(context.Context, sqlx.Session, *UserRoles) error
	}

	customUserRolesModel struct {
		*defaultUserRolesModel
	}
)

func (m *customUserRolesModel) ListRoleIDsTx(ctx context.Context, tx sqlx.Session, tenant, user string) ([]string, error) {
	rows := []string{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select role_id::text from %s where tenant_id=$1 and user_id=$2 order by role_id", m.table), tenant, user)
	return rows, err
}
func (m *customUserRolesModel) ListUserIDsTx(ctx context.Context, tx sqlx.Session, tenant, role string) ([]string, error) {
	rows := []string{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select user_id::text from %s where tenant_id=$1 and role_id=$2 order by user_id limit 501", m.table), tenant, role)
	return rows, err
}
func (m *customUserRolesModel) DeleteByUserTx(ctx context.Context, tx sqlx.Session, tenant, user string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("delete from %s where tenant_id=$1 and user_id=$2", m.table), tenant, user)
	return err
}
func (m *customUserRolesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *UserRoles) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

// NewUserRolesModel returns a model for the database table.
func NewUserRolesModel(conn sqlx.SqlConn) UserRolesModel {
	return &customUserRolesModel{
		defaultUserRolesModel: newUserRolesModel(conn),
	}
}

func (m *customUserRolesModel) withSession(session sqlx.Session) UserRolesModel {
	return NewUserRolesModel(sqlx.NewSqlConnFromSession(session))
}
