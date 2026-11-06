package model

import (
	"context"
	"fmt"
	"github.com/lib/pq"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ SourceObjectsModel = (*customSourceObjectsModel)(nil)

type (
	// SourceObjectsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customSourceObjectsModel.
	SourceObjectsModel interface {
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
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and version_id=$2 and store_id=$3 and object_key=any($4::text[])", sourceObjectsRows, m.table), tenant, version, store, pq.Array(keys))
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
	return &row, err
}
func (m *customSourceObjectsModel) FindOneByStorageTx(ctx context.Context, tx sqlx.Session, store, key string) (*SourceObjects, error) {
	var row SourceObjects
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where store_id=$1 and object_key=$2", sourceObjectsRows, m.table), store, key)
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
	err := tx.QueryRowsCtx(ctx, &rows, fmt.Sprintf("select %s from %s where tenant_id=$1 and store_id=$2 and object_key=any($3::text[])", sourceObjectsRows, m.table), tenant, store, pq.Array(keys))
	return rows, err
}
