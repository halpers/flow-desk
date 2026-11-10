package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"strings"
	"time"
)

type RunsModel interface {
	FindHistoryScheduleCandidateTx(context.Context, sqlx.Session, string, string, int64) (*Runs, error)
	ExistsHistoryScheduleAgeTx(context.Context, sqlx.Session, string, string, int64) (bool, error)
	FindLifecycleInventoryTx(context.Context, sqlx.Session, string, string) (RunLifecycleInventory, error)
	FindOneForShareTx(context.Context, sqlx.Session, string, string) (*Runs, error)
	ListOperationsTx(context.Context, sqlx.Session, RunOperationFilter) ([]Runs, error)
	FindOneVisibleForShareTx(context.Context, sqlx.Session, RunOperationFilter) (*Runs, error)
	FindOneRetryTx(context.Context, sqlx.Session, string, string) (*Runs, error)
	LockTenantQuotaTx(context.Context, sqlx.Session, string) error
	CountActiveTx(context.Context, sqlx.Session, string, string) (int64, int64, error)
	FindActiveByConversationTx(context.Context, sqlx.Session, string, string) (*Runs, error)
	runsModel
	withSession(sqlx.Session) RunsModel

	InsertTx(context.Context, sqlx.Session, *Runs) error
	FindOneOwnedForShareTx(context.Context, sqlx.Session, string, string, string) (*Runs, error)
	FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*Runs, error)
	FindActiveByConversation(context.Context, string, string) (*Runs, error)
	UpdateProgressTx(context.Context, sqlx.Session, *Runs) error
	UpdateLeaseTx(context.Context, sqlx.Session, string, int64) error
	UpdateToolCountTx(context.Context, sqlx.Session, string) error
	ListOverdue(context.Context, int64) ([]Runs, error)
	UpdateWaitingTx(context.Context, sqlx.Session, *Runs) error
	UpdateResumeTx(context.Context, sqlx.Session, *Runs) error
}

type RunOperationFilter struct {
	Tenant, Actor, Role, Scope, Status, RunID string
	Before                                    time.Time
	BeforeID                                  string
	Limit                                     int64
}

// Apply resource authorization before pagination. The tenant authorization lock is held by logic.
const runOperationAccess = `r.tenant_id=$1 and ($4='tenant' or r.actor_user_id=$2)
 and not exists(select 1 from business.run_ticket_resources rr
 join business.tickets t on t.tenant_id=rr.tenant_id and t.id=rr.ticket_id
 where rr.tenant_id=r.tenant_id and rr.run_id=r.id and t.team_id is not null
 and $3<>'admin' and not exists(select 1 from business.team_members tm
 where tm.tenant_id=t.tenant_id and tm.team_id=t.team_id and tm.user_id=$2))`

func (m *customRunsModel) ListOperationsTx(ctx context.Context, tx sqlx.Session, filter RunOperationFilter) ([]Runs, error) {
	rows := []Runs{}
	query := fmt.Sprintf(`select r.%s from %s r where %s
 and ($5='' or r.status=$5) and ($6='' or r.id=nullif($6,'')::uuid)
 and (r.created_at,r.id)<($7,$8::uuid) order by r.created_at desc,r.id desc limit $9`, strings.ReplaceAll(runsRows, ",", ",r."), m.table, runOperationAccess)
	err := tx.QueryRowsCtx(ctx, &rows, query, filter.Tenant, filter.Actor, filter.Role, filter.Scope, filter.Status, filter.RunID, filter.Before, filter.BeforeID, filter.Limit)
	return rows, err
}
func (m *customRunsModel) FindOneVisibleForShareTx(ctx context.Context, tx sqlx.Session, filter RunOperationFilter) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select r.%s from %s r where %s and r.id=$5 for share of r`, strings.ReplaceAll(runsRows, ",", ",r."), m.table, runOperationAccess), filter.Tenant, filter.Actor, filter.Role, filter.Scope, filter.RunID)
	return &row, err
}
func (m *customRunsModel) FindOneRetryTx(ctx context.Context, tx sqlx.Session, tenant, parent string) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and retry_of_run_id=$2", runsRows, m.table), tenant, parent)
	return &row, err
}

func (m *customRunsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", runsRows, m.table), tenant, id)
	return &row, err
}

func (m *customRunsModel) LockTenantQuotaTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	_, err := tx.ExecCtx(ctx, "select pg_advisory_xact_lock(hashtextextended($1,2))", tenant)
	return err
}
func (m *customRunsModel) CountActiveTx(ctx context.Context, tx sqlx.Session, tenant, actor string) (int64, int64, error) {
	var counts struct {
		Tenant int64 `db:"tenant_count"`
		Actor  int64 `db:"actor_count"`
	}
	err := tx.QueryRowCtx(ctx, &counts, fmt.Sprintf("select count(*) as tenant_count,count(*) filter(where actor_user_id=$2) as actor_count from %s where tenant_id=$1 and status in ('queued','running','waiting_input','waiting_approval','cancelling')", m.table), tenant, actor)
	return counts.Tenant, counts.Actor, err
}

func (m *customRunsModel) FindActiveByConversationTx(ctx context.Context, tx sqlx.Session, tenant, conversation string) (*Runs, error) {
	return m.withSession(tx).FindActiveByConversation(ctx, tenant, conversation)
}

type customRunsModel struct{ *defaultRunsModel }

func NewRunsModel(conn sqlx.SqlConn) RunsModel { return &customRunsModel{newRunsModel(conn)} }
func (m *customRunsModel) withSession(tx sqlx.Session) RunsModel {
	return NewRunsModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customRunsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Runs) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customRunsModel) FindOneOwnedForShareTx(ctx context.Context, tx sqlx.Session, tenant, actor, id string) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and actor_user_id=$2 and id=$3 for share", runsRows, m.table), tenant, actor, id)
	return &row, err
}
func (m *customRunsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", runsRows, m.table), tenant, id)
	return &row, err
}
func (m *customRunsModel) FindActiveByConversation(ctx context.Context, tenant, conversation string) (*Runs, error) {
	var row Runs
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and conversation_id=$2 and status in ('queued','running','waiting_input','waiting_approval','cancelling')", runsRows, m.table), tenant, conversation)
	return &row, err
}
func (m *customRunsModel) UpdateProgressTx(ctx context.Context, tx sqlx.Session, row *Runs) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$2,version=version+1,last_event_seq=$3,error_code=$4,updated_at=now() where id=$1 and version=$5", m.table), row.Id, row.Status, row.LastEventSeq, row.ErrorCode, row.Version))
}
func (m *customRunsModel) UpdateLeaseTx(ctx context.Context, tx sqlx.Session, id string, generation int64) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set lease_expires_at=least(deadline_at,now()+interval '60 seconds') where id=$1 and lease_generation=$2", m.table), id, generation))
}
func (m *customRunsModel) UpdateToolCountTx(ctx context.Context, tx sqlx.Session, id string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set tool_call_count=tool_call_count+1 where id=$1 and tool_call_count<12", m.table), id))
}
func (m *customRunsModel) ListOverdue(ctx context.Context, limit int64) ([]Runs, error) {
	rows := []Runs{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where (status in ('queued','running') and deadline_at<now()) or (status in ('waiting_input','waiting_approval') and wait_expires_at<now()) order by deadline_at,id limit $1", runsRows, m.table), limit)
	return rows, err
}

func (m *customRunsModel) UpdateWaitingTx(ctx context.Context, tx sqlx.Session, row *Runs) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set wait_expires_at=$2,lease_expires_at=null where id=$1 and version=$3", m.table), row.Id, row.WaitExpiresAt, row.Version))
}
func (m *customRunsModel) UpdateResumeTx(ctx context.Context, tx sqlx.Session, row *Runs) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set dispatch_id=$2,lease_generation=$3,lease_expires_at=null,deadline_at=$4,wait_expires_at=null,resume_data=$5,input_count=$6 where id=$1 and version=$7", m.table), row.Id, row.DispatchId, row.LeaseGeneration, row.DeadlineAt, row.ResumeData, row.InputCount, row.Version))
}

// RunLifecycleInventory is a metadata-only projection, never a deletion authorization.
type RunLifecycleInventory struct {
	MessageCount               int64     `db:"message_count"`
	EventCount                 int64     `db:"event_count"`
	ActionCount                int64     `db:"action_count"`
	ApprovalCount              int64     `db:"approval_count"`
	PendingApprovalCount       int64     `db:"pending_approval_count"`
	PendingInputCount          int64     `db:"pending_input_count"`
	ReceiptCount               int64     `db:"receipt_count"`
	EvidenceCount              int64     `db:"evidence_count"`
	DispatchCount              int64     `db:"dispatch_count"`
	UnsettledDispatchCount     int64     `db:"unsettled_dispatch_count"`
	RetryLinkCount             int64     `db:"retry_link_count"`
	ActiveConversationRunCount int64     `db:"active_conversation_run_count"`
	ResourceReferenceCount     int64     `db:"resource_reference_count"`
	LeaseActive                bool      `db:"lease_active"`
	OldEnough                  bool      `db:"old_enough"`
	ObservedAt                 time.Time `db:"observed_at"`
}

func (m *customRunsModel) FindLifecycleInventoryTx(ctx context.Context, tx sqlx.Session, tenant, run string) (RunLifecycleInventory, error) {
	var data RunLifecycleInventory
	err := tx.QueryRowCtx(ctx, &data, `select
        (select count(*) from business.messages x where x.tenant_id=r.tenant_id and x.run_id=r.id) message_count,
        (select count(*) from business.run_events x where x.tenant_id=r.tenant_id and x.run_id=r.id) event_count,
        (select count(*) from business.actions x where x.tenant_id=r.tenant_id and x.run_id=r.id) action_count,
        (select count(*) from business.approvals x where x.tenant_id=r.tenant_id and x.run_id=r.id) approval_count,
        (select count(*) from business.approvals x where x.tenant_id=r.tenant_id and x.run_id=r.id and x.status in ('prepared','pending','approved')) pending_approval_count,
        (select count(*) from business.pending_inputs x where x.tenant_id=r.tenant_id and x.run_id=r.id and x.status='pending') pending_input_count,
        (select count(*) from business.action_receipts x join business.actions a on a.tenant_id=x.tenant_id and a.id=x.action_id where a.tenant_id=r.tenant_id and a.run_id=r.id) receipt_count,
        (select count(*) from business.run_evidence x where x.tenant_id=r.tenant_id and x.run_id=r.id) evidence_count,
        (select count(*) from business.outbox_events x where x.tenant_id=r.tenant_id and x.aggregate_id=r.id and x.topic in ('run.execute','run.resume','run.cancel')) dispatch_count,
        (select count(*) from business.outbox_events x where x.tenant_id=r.tenant_id and x.aggregate_id=r.id and x.topic in ('run.execute','run.resume','run.cancel') and (x.status<>'delivered' or x.lease_expires_at>now())) unsettled_dispatch_count,
        (select count(*) from business.runs x where x.tenant_id=r.tenant_id and (x.retry_of_run_id=r.id or x.id=r.retry_of_run_id)) retry_link_count,
        (select count(*) from business.runs x where x.tenant_id=r.tenant_id and x.conversation_id=r.conversation_id and x.status in ('queued','running','waiting_input','waiting_approval','cancelling')) active_conversation_run_count,
        (select count(*) from business.run_ticket_resources x where x.tenant_id=r.tenant_id and x.run_id=r.id) resource_reference_count,
        coalesce(r.lease_expires_at>now(),false) lease_active,
        r.updated_at<=now()-interval '30 days' old_enough, now() observed_at
        from business.runs r where r.tenant_id=$1 and r.id=$2`, tenant, run)
	return data, err
}

func (m *customRunsModel) FindHistoryScheduleCandidateTx(ctx context.Context, tx sqlx.Session, tenant, after string, days int64) (*Runs, error) {
	var row Runs
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`select r.%s from %s r where r.tenant_id=$1 and ($2='' or r.id>nullif($2,'')::uuid) and r.status in ('succeeded','failed','expired','cancelled') and r.updated_at<=clock_timestamp()-$3::bigint*interval '1 day' and not exists(select 1 from business.run_history_operations h where h.tenant_id=r.tenant_id and h.run_id=r.id) order by r.id limit 1`, strings.ReplaceAll(runsRows, ",", ",r."), m.table), tenant, after, days)
	return &row, err
}
func (m *customRunsModel) ExistsHistoryScheduleAgeTx(ctx context.Context, tx sqlx.Session, tenant, id string, days int64) (bool, error) {
	var found bool
	err := tx.QueryRowCtx(ctx, &found, fmt.Sprintf(`select exists(select 1 from %s where tenant_id=$1 and id=$2 and updated_at<=clock_timestamp()-$3::bigint*interval '1 day')`, m.table), tenant, id, days)
	return found, err
}
