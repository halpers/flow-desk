package model

import (
	"context"
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

func (m *customUsersModel) FindOneTx(ctx context.Context, tx sqlx.Session, id string) (*Users, error) {
	return m.withSession(tx).FindOne(ctx, id)
}

func (m *customUsersModel) withSession(session sqlx.Session) UsersModel {
	return NewUsersModel(sqlx.NewSqlConnFromSession(session))
}
