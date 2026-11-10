package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type InvestigationAutomationPoliciesModel interface {
	investigationAutomationPoliciesModel
	InsertIfAbsentTx(context.Context, sqlx.Session, string, string, int64) error
	FindOneForShareTx(context.Context, sqlx.Session, string) (*InvestigationAutomationPolicies, error)
	FindOneForUpdateTx(context.Context, sqlx.Session, string) (*InvestigationAutomationPolicies, error)
	FindDueForUpdateTx(context.Context, sqlx.Session) (*InvestigationAutomationPolicies, error)
	UpdatePolicyTx(context.Context, sqlx.Session, *InvestigationAutomationPolicies) error
	UpdateCheckedTx(context.Context, sqlx.Session, *InvestigationAutomationPolicies, string) error
}
type customInvestigationAutomationPoliciesModel struct {
	*defaultInvestigationAutomationPoliciesModel
}

func NewInvestigationAutomationPoliciesModel(conn sqlx.SqlConn) InvestigationAutomationPoliciesModel {
	return &customInvestigationAutomationPoliciesModel{newInvestigationAutomationPoliciesModel(conn)}
}
func (m *customInvestigationAutomationPoliciesModel) InsertIfAbsentTx(ctx context.Context, tx sqlx.Session, tenant, owner string, version int64) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`insert into %s(id,tenant_id,owner_user_id,owner_auth_version) values($1,$1,$2,$3) on conflict(id) do nothing`, m.table), tenant, owner, version)
	return err
}
func (m *customInvestigationAutomationPoliciesModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant string) (*InvestigationAutomationPolicies, error) {
	var row InvestigationAutomationPolicies
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where tenant_id=$1 for share`, investigationAutomationPoliciesRows, m.table), tenant)
	return &row, err
}
func (m *customInvestigationAutomationPoliciesModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant string) (*InvestigationAutomationPolicies, error) {
	var row InvestigationAutomationPolicies
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where tenant_id=$1 for update`, investigationAutomationPoliciesRows, m.table), tenant)
	return &row, err
}
func (m *customInvestigationAutomationPoliciesModel) FindDueForUpdateTx(ctx context.Context, tx sqlx.Session) (*InvestigationAutomationPolicies, error) {
	var row InvestigationAutomationPolicies
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where enabled and next_run_at<=clock_timestamp() order by next_run_at,id limit 1 for update skip locked`, investigationAutomationPoliciesRows, m.table))
	return &row, err
}
func (m *customInvestigationAutomationPoliciesModel) UpdatePolicyTx(ctx context.Context, tx sqlx.Session, row *InvestigationAutomationPolicies) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set enabled=$3,idle_days=$4,interval_minutes=$5,owner_user_id=$6,owner_auth_version=$7,revision=revision+1,next_run_at=clock_timestamp()+$5::bigint*interval '1 minute',updated_at=clock_timestamp() where tenant_id=$1 and revision=$2`, m.table), row.TenantId, row.Revision, row.Enabled, row.IdleDays, row.IntervalMinutes, row.OwnerUserId, row.OwnerAuthVersion))
}
func (m *customInvestigationAutomationPoliciesModel) UpdateCheckedTx(ctx context.Context, tx sqlx.Session, row *InvestigationAutomationPolicies, outcome string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set cursor_id=$3,last_outcome=$4,last_checked_at=clock_timestamp(),next_run_at=clock_timestamp()+interval_minutes*interval '1 minute',last_investigation_id=$5,last_revision=$6,updated_at=clock_timestamp() where tenant_id=$1 and revision=$2 and enabled`, m.table), row.TenantId, row.Revision, row.CursorId, outcome, row.LastInvestigationId, row.LastRevision))
}
