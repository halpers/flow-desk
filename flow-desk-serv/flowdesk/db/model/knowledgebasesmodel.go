package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ KnowledgeBasesModel = (*customKnowledgeBasesModel)(nil)

type (
	// KnowledgeBasesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customKnowledgeBasesModel.
	KnowledgeBasesModel interface {
		knowledgeBasesModel
		withSession(session sqlx.Session) KnowledgeBasesModel
		InsertTx(context.Context, sqlx.Session, *KnowledgeBases) error
		CountByTenantTx(context.Context, sqlx.Session, string) (int64, error)
		LockTenantQuotaTx(context.Context, sqlx.Session, string) error
		ListByTenant(context.Context, string) ([]KnowledgeBases, error)
		ListAccessibleTx(context.Context, sqlx.Session, string, string, string) ([]KnowledgeBases, error)
		FindOneForShareTx(context.Context, sqlx.Session, string, string) (*KnowledgeBases, error)
		FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*KnowledgeBases, error)
		UpdateAclTx(context.Context, sqlx.Session, string, string, int64, string) error
		UpdateTeamTx(context.Context, sqlx.Session, *KnowledgeBases) error
		UpdateRetentionPolicyTx(context.Context, sqlx.Session, *KnowledgeBases, int64, int64) error
	}

	customKnowledgeBasesModel struct {
		*defaultKnowledgeBasesModel
	}
)

// NewKnowledgeBasesModel returns a model for the database table.
func NewKnowledgeBasesModel(conn sqlx.SqlConn) KnowledgeBasesModel {
	return &customKnowledgeBasesModel{
		defaultKnowledgeBasesModel: newKnowledgeBasesModel(conn),
	}
}

func (m *customKnowledgeBasesModel) InsertTx(ctx context.Context, tx sqlx.Session, row *KnowledgeBases) error {
	// New knowledge bases always inherit database policy defaults.
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("insert into %s(id,tenant_id,name,read_roles,acl_revision,team_id) values($1,$2,$3,$4,$5,$6)", m.table), row.Id, row.TenantId, row.Name, row.ReadRoles, row.AclRevision, row.TeamId)
	return err
}
func (m *customKnowledgeBasesModel) CountByTenantTx(ctx context.Context, tx sqlx.Session, tenant string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1", m.table), tenant)
	return count, err
}
func (m *customKnowledgeBasesModel) LockTenantQuotaTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	_, err := tx.ExecCtx(ctx, "select pg_advisory_xact_lock(hashtextextended($1,6))", tenant)
	return err
}
func (m *customKnowledgeBasesModel) ListByTenant(ctx context.Context, tenant string) ([]KnowledgeBases, error) {
	rows := []KnowledgeBases{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 order by created_at,id limit 51", knowledgeBasesRows, m.table), tenant)
	return rows, err
}
func (m *customKnowledgeBasesModel) ListAccessibleTx(ctx context.Context, tx sqlx.Session, tenant, actor, role string) ([]KnowledgeBases, error) {
	rows := []KnowledgeBases{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and read_roles ? $2 and %s order by created_at,id limit 51 for share", knowledgeBasesRows, m.table, fmt.Sprintf(teamAccessPredicate, 2, "knowledge_bases", 3)), tenant, role, actor)
	return rows, err
}
func (m *customKnowledgeBasesModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*KnowledgeBases, error) {
	var row KnowledgeBases
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", knowledgeBasesRows, m.table), tenant, id)
	return &row, err
}
func (m *customKnowledgeBasesModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*KnowledgeBases, error) {
	var row KnowledgeBases
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", knowledgeBasesRows, m.table), tenant, id)
	return &row, err
}
func (m *customKnowledgeBasesModel) UpdateAclTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64, roles string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set read_roles=$4,acl_revision=acl_revision+1 where tenant_id=$1 and id=$2 and acl_revision=$3", m.table), tenant, id, revision, roles))
}

func (m *customKnowledgeBasesModel) withSession(session sqlx.Session) KnowledgeBasesModel {
	return NewKnowledgeBasesModel(sqlx.NewSqlConnFromSession(session))
}
func (m *customKnowledgeBasesModel) UpdateTeamTx(ctx context.Context, tx sqlx.Session, row *KnowledgeBases) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set team_id=$3,acl_revision=acl_revision+1 where tenant_id=$1 and id=$2 and acl_revision=$4", m.table), row.TenantId, row.Id, row.TeamId, row.AclRevision))
}

func (m *customKnowledgeBasesModel) UpdateRetentionPolicyTx(ctx context.Context, tx sqlx.Session, row *KnowledgeBases, keepLatest, minimumAgeDays int64) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set retention_keep_latest=$4,retention_minimum_age_days=$5,retention_revision=retention_revision+1 where tenant_id=$1 and id=$2 and retention_revision=$3", m.table), row.TenantId, row.Id, row.RetentionRevision, keepLatest, minimumAgeDays))
}
