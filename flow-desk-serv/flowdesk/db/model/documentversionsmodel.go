package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ DocumentVersionsModel = (*customDocumentVersionsModel)(nil)

type (
	// DocumentVersionsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customDocumentVersionsModel.
	DocumentVersionsModel interface {
		documentVersionsModel
		withSession(session sqlx.Session) DocumentVersionsModel
		InsertTx(context.Context, sqlx.Session, *DocumentVersions) error
		CountByDocumentTx(context.Context, sqlx.Session, string, string) (int64, error)
		ListByDocument(context.Context, string, string) ([]DocumentVersions, error)
		FindOneForShareTx(context.Context, sqlx.Session, string, string) (*DocumentVersions, error)
		FindOneByJobIdTx(context.Context, sqlx.Session, string) (*DocumentVersions, error)
		UpdateIndexTx(context.Context, sqlx.Session, string, string, string, int64, string) error
		ListActiveForShareTx(context.Context, sqlx.Session, string, string, string) ([]ActiveKnowledgeVersion, error)
	}

	customDocumentVersionsModel struct {
		*defaultDocumentVersionsModel
	}
)

// NewDocumentVersionsModel returns a model for the database table.
func NewDocumentVersionsModel(conn sqlx.SqlConn) DocumentVersionsModel {
	return &customDocumentVersionsModel{
		defaultDocumentVersionsModel: newDocumentVersionsModel(conn),
	}
}

type ActiveKnowledgeVersion struct {
	VersionId       string `db:"version_id"`
	DocumentId      string `db:"document_id"`
	KnowledgeBaseId string `db:"knowledge_base_id"`
	Title           string `db:"title"`
	AclRevision     int64  `db:"acl_revision"`
	Content         string `db:"content"`
}

func (m *customDocumentVersionsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *DocumentVersions) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customDocumentVersionsModel) CountByDocumentTx(ctx context.Context, tx sqlx.Session, tenant, document string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1 and document_id=$2", m.table), tenant, document)
	return count, err
}
func (m *customDocumentVersionsModel) FindOneByJobIdTx(ctx context.Context, tx sqlx.Session, job string) (*DocumentVersions, error) {
	return m.withSession(tx).FindOneByJobId(ctx, job)
}
func (m *customDocumentVersionsModel) ListByDocument(ctx context.Context, tenant, document string) ([]DocumentVersions, error) {
	rows := []DocumentVersions{}
	err := m.conn.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and document_id=$2 order by created_at,id limit 101", documentVersionsRows, m.table), tenant, document)
	return rows, err
}
func (m *customDocumentVersionsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*DocumentVersions, error) {
	var row DocumentVersions
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", documentVersionsRows, m.table), tenant, id)
	return &row, err
}
func (m *customDocumentVersionsModel) UpdateIndexTx(ctx context.Context, tx sqlx.Session, version, job, hash string, count int64, status string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set index_status=$5,chunk_count=$4 where id=$1 and job_id=$2 and content_hash=$3 and (index_status='pending' or (index_status=$5 and chunk_count=$4))", m.table), version, job, hash, count, status))
}
func (m *customDocumentVersionsModel) ListActiveForShareTx(ctx context.Context, tx sqlx.Session, tenant, role, actor string) ([]ActiveKnowledgeVersion, error) {
	if err := lockAuthorizationForShareTx(ctx, tx, tenant); err != nil {
		return nil, err
	}
	rows := []ActiveKnowledgeVersion{}
	// The version model owns this main-table query. Shared locks linearize ACL revocation,
	// withdrawal and activation against evidence registration and final citation checks.
	query := fmt.Sprintf(`select v.id as version_id,d.id as document_id,k.id as knowledge_base_id,
 d.title,k.acl_revision,v.content from %s v join business.documents d
 on d.tenant_id=v.tenant_id and d.id=v.document_id and d.active_version_id=v.id
 join business.knowledge_bases k on k.tenant_id=d.tenant_id and k.id=d.knowledge_base_id
 where v.tenant_id=$1 and v.index_status='ready' and d.status='active' and k.read_roles ? $2 and (k.team_id is null or $2='admin' or exists(select 1 from business.team_members tm where tm.tenant_id=k.tenant_id and tm.team_id=k.team_id and tm.user_id=$3))
 order by v.id for share of v,d,k`, m.table)
	err := tx.QueryRowsCtx(ctx, &rows, query, tenant, role, actor)
	return rows, err
}

func (m *customDocumentVersionsModel) withSession(session sqlx.Session) DocumentVersionsModel {
	return NewDocumentVersionsModel(sqlx.NewSqlConnFromSession(session))
}
