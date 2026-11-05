package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ RolesModel = (*customRolesModel)(nil)

type (
	// RolesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customRolesModel.
	RolesModel interface {
		rolesModel
		withSession(session sqlx.Session) RolesModel
		ListByTenantTx(context.Context, sqlx.Session, string) ([]Roles, error)
		InsertTx(context.Context, sqlx.Session, *Roles) error
		UpdateManagedTx(context.Context, sqlx.Session, *Roles) error
	}

	customRolesModel struct {
		*defaultRolesModel
	}
)

func (m *customRolesModel) ListByTenantTx(ctx context.Context, tx sqlx.Session, tenant string) ([]Roles, error) {
	rows := []Roles{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 order by builtin_role desc,name,id limit 54", rolesRows, m.table), tenant)
	return rows, err
}
func (m *customRolesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Roles) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customRolesModel) UpdateManagedTx(ctx context.Context, tx sqlx.Session, row *Roles) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set name=$4,permissions=$5,enabled=$6,revision=revision+1 where tenant_id=$1 and id=$2 and revision=$3 and builtin_role=''", m.table), row.TenantId, row.Id, row.Revision, row.Name, row.Permissions, row.Enabled))
}

// NewRolesModel returns a model for the database table.
func NewRolesModel(conn sqlx.SqlConn) RolesModel {
	return &customRolesModel{
		defaultRolesModel: newRolesModel(conn),
	}
}

func (m *customRolesModel) withSession(session sqlx.Session) RolesModel {
	return NewRolesModel(sqlx.NewSqlConnFromSession(session))
}
