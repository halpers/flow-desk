package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type ApprovalsModel interface {
	approvalsModel
	withSession(sqlx.Session) ApprovalsModel

	InsertTx(ctx context.Context, tx sqlx.Session, row *Approvals) error
	FindOneByActionForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, action string) (*Approvals, error)
	ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]Approvals, error)
	UpdateDecisionTx(ctx context.Context, tx sqlx.Session, row *Approvals) error
}
type customApprovalsModel struct{ *defaultApprovalsModel }

func NewApprovalsModel(conn sqlx.SqlConn) ApprovalsModel {
	return &customApprovalsModel{newApprovalsModel(conn)}
}
func (m *customApprovalsModel) withSession(tx sqlx.Session) ApprovalsModel {
	return NewApprovalsModel(sqlx.NewSqlConnFromSession(tx))
}
func (m *customApprovalsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Approvals) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}

func (m *customApprovalsModel) FindOneByActionForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, action string) (*Approvals, error) {
	var row Approvals
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and action_id=$2 for update", approvalsRows, m.table), tenant, action)
	return &row, err
}
func (m *customApprovalsModel) ListByRunTx(ctx context.Context, tx sqlx.Session, tenant, run string) ([]Approvals, error) {
	rows := []Approvals{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 order by created_at,id", approvalsRows, m.table), tenant, run)
	return rows, err
}
func (m *customApprovalsModel) UpdateDecisionTx(ctx context.Context, tx sqlx.Session, row *Approvals) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$3,decided_by=$4,decision_reason=$5,decided_at=$6,version=version+1 where tenant_id=$1 and id=$2 and version=$7", m.table), row.TenantId, row.Id, row.Status, row.DecidedBy, row.DecisionReason, row.DecidedAt, row.Version))
}
