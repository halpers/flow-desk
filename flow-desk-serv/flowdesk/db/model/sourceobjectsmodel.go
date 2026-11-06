package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ SourceObjectsModel = (*customSourceObjectsModel)(nil)

type (
	// SourceObjectsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customSourceObjectsModel.
	SourceObjectsModel interface {
		ListHistoryTx(context.Context, sqlx.Session, string, string, time.Time, string, int64) ([]SourceCopyHistoryRow, error)
		FindHistoryItemTx(context.Context, sqlx.Session, string, string, string) (*SourceCopyHistoryRow, error)
		DeleteRetiredTx(context.Context, sqlx.Session, string, string) error
		ListTenantInventoryRegistrationsTx(context.Context, sqlx.Session, string, string, []string) ([]SourceObjects, error)
		ListInventoryRegistrationsTx(context.Context, sqlx.Session, string, string, string, []string) ([]SourceObjects, error)
		InsertIfAbsentTx(context.Context, sqlx.Session, *SourceObjects) error
		FindOneForUpdateTx(context.Context, sqlx.Session, string, string) (*SourceObjects, error)
		FindOneByStorageTx(context.Context, sqlx.Session, string, string) (*SourceObjects, error)
		ListByVersionTx(context.Context, sqlx.Session, string, string) ([]SourceObjects, error)
		CountRegisteredTx(context.Context, sqlx.Session, string, string) (int64, error)
		UpdateStatusTx(context.Context, sqlx.Session, string, string, string, string) error
		sourceObjectsModel
		withSession(session sqlx.Session) SourceObjectsModel
	}

	customSourceObjectsModel struct {
		*defaultSourceObjectsModel
	}
)

// NewSourceObjectsModel returns a model for the database table.
func NewSourceObjectsModel(conn sqlx.SqlConn) SourceObjectsModel {
	return &customSourceObjectsModel{
		defaultSourceObjectsModel: newSourceObjectsModel(conn),
	}
}

func (m *customSourceObjectsModel) ListInventoryRegistrationsTx(ctx context.Context, tx sqlx.Session, tenant, version, store string, keys []string) ([]SourceObjects, error) {
	rows := []SourceObjects{}
	if len(keys) == 0 {
		return rows, nil
	}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and version_id=$2 and store_id=$3 and object_key=any($4::text[])", sourceObjectsRows, m.sourceIdentityRows()), tenant, version, store, pq.Array(keys))
	return rows, err
}

func (m *customSourceObjectsModel) InsertIfAbsentTx(ctx context.Context, tx sqlx.Session, row *SourceObjects) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`insert into %s(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size)
 values($1,$2,$3,$4,$5,$6,$7,$8) on conflict(store_id,object_key) do nothing`, m.table), row.Id, row.TenantId, row.VersionId, row.SourceEpoch, row.StoreId, row.ObjectKey, row.ContentHash, row.SourceSize)
	return err
}
func (m *customSourceObjectsModel) FindOneForUpdateTx(ctx context.Context, tx sqlx.Session, tenant, id string) (*SourceObjects, error) {
	var row SourceObjects
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2 for update", sourceObjectsRows, m.table), tenant, id)
	if errors.Is(err, ErrNotFound) {
		err = tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from business.source_object_archives where tenant_id=$1 and id=$2 for update", sourceObjectsRows), tenant, id)
	}
	return &row, err
}
func (m *customSourceObjectsModel) FindOneByStorageTx(ctx context.Context, tx sqlx.Session, store, key string) (*SourceObjects, error) {
	var row SourceObjects
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where store_id=$1 and object_key=$2", sourceObjectsRows, m.sourceIdentityRows()), store, key)
	return &row, err
}
func (m *customSourceObjectsModel) ListByVersionTx(ctx context.Context, tx sqlx.Session, tenant, version string) ([]SourceObjects, error) {
	rows := []SourceObjects{}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and version_id=$2 order by (status='retired'),created_at desc,id desc limit 101", sourceObjectsRows, m.table), tenant, version)
	return rows, err
}
func (m *customSourceObjectsModel) CountRegisteredTx(ctx context.Context, tx sqlx.Session, tenant, version string) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf("select count(*) from %s where tenant_id=$1 and version_id=$2 and status<>'retired'", m.table), tenant, version)
	return count, err
}
func (m *customSourceObjectsModel) UpdateStatusTx(ctx context.Context, tx sqlx.Session, tenant, id, before, after string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("update %s set status=$4,retired_at=case when $4='retired' then now() else retired_at end where tenant_id=$1 and id=$2 and status=$3", m.table), tenant, id, before, after))
}

func (m *customSourceObjectsModel) withSession(session sqlx.Session) SourceObjectsModel {
	return NewSourceObjectsModel(sqlx.NewSqlConnFromSession(session))
}

func (m *customSourceObjectsModel) ListTenantInventoryRegistrationsTx(ctx context.Context, tx sqlx.Session, tenant, store string, keys []string) ([]SourceObjects, error) {
	rows := []SourceObjects{}
	if len(keys) == 0 {
		return rows, nil
	}
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and store_id=$2 and object_key=any($3::text[])", sourceObjectsRows, m.sourceIdentityRows()), tenant, store, pq.Array(keys))
	return rows, err
}

// Retired rows move atomically; compatible identity reads preserve receipts,
// registration deduplication and storage reconciliation across both tables.
func (m *customSourceObjectsModel) sourceIdentityRows() string {
	return fmt.Sprintf("(select %s from %s union all select %s from business.source_object_archives) source_identities", sourceObjectsRows, m.table, sourceObjectsRows)
}
func (m *customSourceObjectsModel) FindOne(ctx context.Context, id string) (*SourceObjects, error) {
	var row SourceObjects
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where id=$1", sourceObjectsRows, m.sourceIdentityRows()), id)
	return &row, err
}
func (m *customSourceObjectsModel) FindOneByTenantIdId(ctx context.Context, tenant, id string) (*SourceObjects, error) {
	var row SourceObjects
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and id=$2", sourceObjectsRows, m.sourceIdentityRows()), tenant, id)
	return &row, err
}
func (m *customSourceObjectsModel) FindOneByStoreIdObjectKey(ctx context.Context, store, key string) (*SourceObjects, error) {
	var row SourceObjects
	err := m.conn.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where store_id=$1 and object_key=$2", sourceObjectsRows, m.sourceIdentityRows()), store, key)
	return &row, err
}
func (m *customSourceObjectsModel) DeleteRetiredTx(ctx context.Context, tx sqlx.Session, tenant, id string) error {
	return requireOne(tx.ExecCtx(ctx, fmt.Sprintf("delete from %s where tenant_id=$1 and id=$2 and status='retired'", m.table), tenant, id))
}

type SourceCopyHistoryRow struct {
	Id                 string       `db:"id"`
	SourceEpoch        int64        `db:"source_epoch"`
	SourceSize         int64        `db:"source_size"`
	Status             string       `db:"status"`
	CreatedAt          time.Time    `db:"created_at"`
	RetiredAt          sql.NullTime `db:"retired_at"`
	MetadataArchived   bool         `db:"metadata_archived"`
	ArchivedAt         sql.NullTime `db:"archived_at"`
	ArchiveBlockReason string       `db:"archive_block_reason"`
}

func (m *customSourceObjectsModel) sourceHistoryQuery() string {
	return fmt.Sprintf(`select r.id,r.source_epoch,r.source_size,r.status,r.created_at,r.retired_at,r.metadata_archived,r.archived_at,
    case when r.metadata_archived then 'already_archived'
         when r.status<>'retired' then 'not_retired'
         when r.retired_at is null then 'retirement_incomplete'
         when r.source_epoch>=v.source_epoch or (v.source_storage='s3' and v.source_store_id=r.store_id and v.source_key=r.object_key) then 'current_generation'
         when exists(select 1 from business.outbox_events e where e.tenant_id=r.tenant_id and e.topic='knowledge.source_cleanup' and e.payload->>'object_id'=r.id::text and e.status<>'delivered') then 'cleanup_delivery_pending'
         else '' end as archive_block_reason
    from (select %s,false as metadata_archived,null::timestamptz as archived_at from %s
        union all select %s,true as metadata_archived,archived_at from business.source_object_archives) r
    join business.document_versions v on v.tenant_id=r.tenant_id and v.id=r.version_id
    where r.tenant_id=$1 and r.version_id=$2`, sourceObjectsRows, m.table, sourceObjectsRows)
}
func (m *customSourceObjectsModel) ListHistoryTx(ctx context.Context, tx sqlx.Session, tenant, version string, before time.Time, beforeID string, limit int64) ([]SourceCopyHistoryRow, error) {
	rows := []SourceCopyHistoryRow{}
	err := tx.QueryRowsCtx(ctx, &rows, m.sourceHistoryQuery()+" and (r.created_at,r.id)<($3,$4::uuid) order by r.created_at desc,r.id desc limit $5", tenant, version, before, beforeID, limit)
	return rows, err
}
func (m *customSourceObjectsModel) FindHistoryItemTx(ctx context.Context, tx sqlx.Session, tenant, version, id string) (*SourceCopyHistoryRow, error) {
	var row SourceCopyHistoryRow
	err := tx.QueryRowCtx(ctx, &row, m.sourceHistoryQuery()+" and r.id=$3::uuid", tenant, version, id)
	return &row, err
}
