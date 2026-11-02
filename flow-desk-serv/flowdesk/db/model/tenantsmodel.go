package model

import "github.com/zeromicro/go-zero/core/stores/sqlx"

var _ TenantsModel = (*customTenantsModel)(nil)

type (
	// TenantsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customTenantsModel.
	TenantsModel interface {
		tenantsModel
		withSession(session sqlx.Session) TenantsModel
	}

	customTenantsModel struct {
		*defaultTenantsModel
	}
)

// NewTenantsModel returns a model for the database table.
func NewTenantsModel(conn sqlx.SqlConn) TenantsModel {
	return &customTenantsModel{
		defaultTenantsModel: newTenantsModel(conn),
	}
}

func (m *customTenantsModel) withSession(session sqlx.Session) TenantsModel {
	return NewTenantsModel(sqlx.NewSqlConnFromSession(session))
}
