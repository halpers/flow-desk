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
		UpdateArchivedSourceTx(context.Context, sqlx.Session, *DocumentVersions, string, string) error
		UpdateRestoredSourceTx(context.Context, sqlx.Session, *DocumentVersions, []byte) error
		documentVersionsModel
		withSession(session sqlx.Session) DocumentVersionsModel
		InsertTx(context.Context, sqlx.Session, *DocumentVersions) error
		CountByDocumentTx(context.Context, sqlx.Session, string, string) (int64, error)
		ListByDocument(context.Context, string, string) ([]DocumentVersionSummary, error)
		FindOneForShareTx(context.Context, sqlx.Session, string, string) (*DocumentVersions, error)
		FindOneByJobIdForUpdateTx(context.Context, sqlx.Session, string) (*DocumentVersions, error)
		UpdateIndexResultTx(context.Context, sqlx.Session, *DocumentVersions) error
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
	SourceFormat    string `db:"source_format"`
	PageTexts       string `db:"page_texts"`
}

// Listing metadata must not load every original PDF into API memory.
type DocumentVersionSummary struct {
	SourceStorage string `db:"source_storage"`
	OcrMode       string `db:"ocr_mode"`
	OcrPageCount  int64  `db:"ocr_page_count"`
	Id            string `db:"id"`
	DocumentId    string `db:"document_id"`
	Filename      string `db:"filename"`
	ContentHash   string `db:"content_hash"`
	IndexStatus   string `db:"index_status"`
	ChunkCount    int64  `db:"chunk_count"`
	ErrorCode     string `db:"error_code"`
	SourceFormat  string `db:"source_format"`
	SourceSize    int64  `db:"source_size"`
	PageCount     int64  `db:"page_count"`
	TextPageCount int64  `db:"text_page_count"`
}

func (m *customDocumentVersionsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *DocumentVersions) error {
	if row.SourceStorage == "" {
		row.SourceStorage = "postgres"
	}
	if row.SourceSize == 0 {
		if row.SourceFormat == "pdf" {
			row.SourceSize = int64(len(row.SourceBytes))
		} else {
			row.SourceSize = int64(len(row.Content))
		}
	}
	if row.OcrMode == "" {
		row.OcrMode = "off"
	}
	if row.SourceFormat == "" {
		row.SourceFormat = "text"
	}
	if row.SourceBytes == nil {
		row.SourceBytes = []byte{}
	}
	if row.PageTexts == "" {
		row.PageTexts = "[]"
	}
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customDocumentVersionsModel) CountByDocumentTx(ctx context.Context, tx sqlx.Session, tenant, document string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1 and document_id=$2", m.table), tenant, document)
	return count, err
}
func (m *customDocumentVersionsModel) FindOneByJobIdForUpdateTx(ctx context.Context, tx sqlx.Session, job string) (*DocumentVersions, error) {
	var row DocumentVersions
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where job_id=$1 for update", documentVersionsRows, m.table), job)
	return &row, err
}
func (m *customDocumentVersionsModel) UpdateIndexResultTx(ctx context.Context, tx sqlx.Session, row *DocumentVersions) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set content=$4,page_texts=$5,parser_version=$6,index_status=$7,chunk_count=$8,error_code=$9 where id=$1 and job_id=$2 and content_hash=$3 and index_status='pending'", m.table), row.Id, row.JobId, row.ContentHash, row.Content, row.PageTexts, row.ParserVersion, row.IndexStatus, row.ChunkCount, row.ErrorCode))
}
func (m *customDocumentVersionsModel) ListByDocument(ctx context.Context, tenant, document string) ([]DocumentVersionSummary, error) {
	rows := []DocumentVersionSummary{}
	query := fmt.Sprintf(`select id,document_id,filename,content_hash,index_status,chunk_count,error_code,source_format,ocr_mode,
    source_size,source_storage,
    jsonb_array_length(page_texts) as page_count,
    (select count(*) from jsonb_array_elements(page_texts) p where p->>'text' ~ '[^[:space:]]') as text_page_count,
 (select count(*) from jsonb_array_elements(page_texts) p where p->>'extraction_method'='ocr') as ocr_page_count
    from %s where tenant_id=$1 and document_id=$2 order by created_at,id limit 101`, m.table)
	err := m.conn.QueryRowsCtx(ctx, &rows, query, tenant, document)
	return rows, err
}

func (m *customDocumentVersionsModel) UpdateArchivedSourceTx(ctx context.Context, tx sqlx.Session, row *DocumentVersions, store, key string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set source_storage='s3',source_store_id=$4,source_key=$5,source_bytes=''::bytea where tenant_id=$1 and id=$2 and source_epoch=$3 and source_storage='postgres'", m.table), row.TenantId, row.Id, row.SourceEpoch, store, key))
}
func (m *customDocumentVersionsModel) UpdateRestoredSourceTx(ctx context.Context, tx sqlx.Session, row *DocumentVersions, raw []byte) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set source_storage='postgres',source_store_id='',source_key='',source_bytes=$4,source_epoch=source_epoch+1 where tenant_id=$1 and id=$2 and source_epoch=$3 and source_storage='s3'", m.table), row.TenantId, row.Id, row.SourceEpoch, raw))
}
func (m *customDocumentVersionsModel) FindOneForShareTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*DocumentVersions, error) {
	var row DocumentVersions
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for share", documentVersionsRows, m.table), tenant, id)
	return &row, err
}
func (m *customDocumentVersionsModel) ListActiveForShareTx(ctx context.Context, tx sqlx.Session, tenant, role, actor string) ([]ActiveKnowledgeVersion, error) {
	if err := lockAuthorizationForShareTx(ctx, tx, tenant); err != nil {
		return nil, err
	}
	rows := []ActiveKnowledgeVersion{}
	// The version model owns this main-table query. Shared locks linearize ACL revocation,
	// withdrawal and activation against evidence registration and final citation checks.
	query := fmt.Sprintf(`select v.id as version_id,d.id as document_id,k.id as knowledge_base_id,
 d.title,k.acl_revision,v.content,v.source_format,v.page_texts from %s v join business.documents d
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
