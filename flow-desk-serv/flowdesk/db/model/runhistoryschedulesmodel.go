package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type RunHistorySchedulesModel interface {
	runHistorySchedulesModel
	InsertIfAbsentTx(context.Context, sqlx.Session, string, string, int64) error
	FindOneForShareTx(context.Context, sqlx.Session, string) (*RunHistorySchedules, error)
	FindOneForUpdateTx(context.Context, sqlx.Session, string) (*RunHistorySchedules, error)
	FindDueForUpdateTx(context.Context, sqlx.Session) (*RunHistorySchedules, error)
	FindClaimForShareTx(context.Context, sqlx.Session, *RunHistorySchedules) (*RunHistorySchedules, error)
	UpdatePolicyTx(context.Context, sqlx.Session, *RunHistorySchedules) error
	UpdateClaimTx(context.Context, sqlx.Session, *RunHistorySchedules, string) error
	UpdateIntentTx(context.Context, sqlx.Session, *RunHistorySchedules, string, string) error
	UpdateFinishedTx(context.Context, sqlx.Session, *RunHistorySchedules, string) error
}
type customRunHistorySchedulesModel struct {
	*defaultRunHistorySchedulesModel
}

func NewRunHistorySchedulesModel(conn sqlx.SqlConn) RunHistorySchedulesModel {
	return &customRunHistorySchedulesModel{newRunHistorySchedulesModel(conn)}
}
func (m *customRunHistorySchedulesModel) InsertIfAbsentTx(ctx context.Context, tx sqlx.Session, tenant, actor string, version int64) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`insert into %s(id,tenant_id,owner_user_id,owner_auth_version) values($1,$1,$2,$3) on conflict(id) do nothing`, m.table), tenant, actor, version)
	return err
}
func (m *customRunHistorySchedulesModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant string) (*RunHistorySchedules, error) {
	var row RunHistorySchedules
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where tenant_id=$1 for share`, runHistorySchedulesRows, m.table), tenant)
	return &row, err
}
func (m *customRunHistorySchedulesModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant string) (*RunHistorySchedules, error) {
	var row RunHistorySchedules
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where tenant_id=$1 for update`, runHistorySchedulesRows, m.table), tenant)
	return &row, err
}
func (m *customRunHistorySchedulesModel) FindDueForUpdateTx(ctx context.Context, tx sqlx.Session) (*RunHistorySchedules, error) {
	var row RunHistorySchedules
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where enabled and next_run_at<=clock_timestamp() and (lease_until is null or lease_until<=clock_timestamp()) order by next_run_at,id limit 1 for update skip locked`, runHistorySchedulesRows, m.table))
	return &row, err
}
func (m *customRunHistorySchedulesModel) FindClaimForShareTx(ctx context.Context, tx sqlx.Session, claim *RunHistorySchedules) (*RunHistorySchedules, error) {
	var row RunHistorySchedules
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select %s from %s where tenant_id=$1 and revision=$2 and enabled and lease_token=$3 and lease_until>clock_timestamp() for share`, runHistorySchedulesRows, m.table), claim.TenantId, claim.Revision, claim.LeaseToken)
	return &row, err
}
func (m *customRunHistorySchedulesModel) UpdatePolicyTx(ctx context.Context, tx sqlx.Session, row *RunHistorySchedules) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set enabled=$3,minimum_age_days=$4,interval_minutes=$5,owner_user_id=$6,owner_auth_version=$7,revision=revision+1,next_run_at=clock_timestamp()+$5::bigint*interval '1 minute',lease_token=null,lease_until=null,updated_at=clock_timestamp() where tenant_id=$1 and revision=$2`, m.table), row.TenantId, row.Revision, row.Enabled, row.MinimumAgeDays, row.IntervalMinutes, row.OwnerUserId, row.OwnerAuthVersion))
}
func (m *customRunHistorySchedulesModel) UpdateClaimTx(ctx context.Context, tx sqlx.Session, row *RunHistorySchedules, token string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set lease_token=$3,lease_until=clock_timestamp()+interval '2 minutes' where tenant_id=$1 and revision=$2`, m.table), row.TenantId, row.Revision, token))
}
func (m *customRunHistorySchedulesModel) UpdateIntentTx(ctx context.Context, tx sqlx.Session, row *RunHistorySchedules, run, operation string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set last_run_id=$4,last_operation_id=$5,cursor_run_id=$4,last_outcome='pending' where tenant_id=$1 and revision=$2 and lease_token=$3 and lease_until>clock_timestamp() and enabled`, m.table), row.TenantId, row.Revision, row.LeaseToken, run, operation))
}
func (m *customRunHistorySchedulesModel) UpdateFinishedTx(ctx context.Context, tx sqlx.Session, row *RunHistorySchedules, outcome string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf(`update %s set last_outcome=$4,last_checked_at=clock_timestamp(),cursor_run_id=$5,next_run_at=clock_timestamp()+interval_minutes*interval '1 minute',lease_token=null,lease_until=null,updated_at=clock_timestamp() where tenant_id=$1 and revision=$2 and lease_token=$3 and lease_until>clock_timestamp() and enabled`, m.table), row.TenantId, row.Revision, row.LeaseToken, outcome, row.CursorRunId))
}
