package model

import (
	"context"
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
	}

	customUsersModel struct {
		*defaultUsersModel
	}
)

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
