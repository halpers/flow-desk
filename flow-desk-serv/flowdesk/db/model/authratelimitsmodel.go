package model

import (
	"context"
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const LoginGlobalBucket = "login_global"
const PasswordGlobalBucket = "password_global"
const PasswordAccountBucket = "password_account"

// AuthRateBucket is a trusted internal key, never a request account or IP.
type AuthRateBucket struct {
	Kind, TenantID, UserID string
}

func (bucket AuthRateBucket) parameters() (string, any, any) {
	if bucket.Kind == PasswordAccountBucket {
		return bucket.Kind + ":" + bucket.TenantID + ":" + bucket.UserID, bucket.TenantID, bucket.UserID
	}
	return bucket.Kind, nil, nil
}

type AuthRateLimitsModel interface {
	authRateLimitsModel
	InsertBucketIfAbsentTx(context.Context, sqlx.Session, AuthRateBucket) error
	FindBucketForUpdateTx(context.Context, sqlx.Session, AuthRateBucket) (*AuthRateLimits, error)
	FindDatabaseTimeTx(context.Context, sqlx.Session) (time.Time, error)
	UpdateScheduleTx(context.Context, sqlx.Session, AuthRateBucket, time.Time, time.Time, time.Time) error
	DeleteReclaimableAccountsTx(context.Context, sqlx.Session, time.Time, time.Time, int64) error
	CountAccountBucketsTx(context.Context, sqlx.Session) (int64, error)
}

type customAuthRateLimitsModel struct{ *defaultAuthRateLimitsModel }

func NewAuthRateLimitsModel(conn sqlx.SqlConn) AuthRateLimitsModel {
	return &customAuthRateLimitsModel{newAuthRateLimitsModel(conn)}
}

func (m *customAuthRateLimitsModel) InsertBucketIfAbsentTx(ctx context.Context, tx sqlx.Session, bucket AuthRateBucket) error {
	id, tenant, user := bucket.parameters()
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`INSERT INTO %s (id,bucket_kind,tenant_id,user_id) VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`, m.table), id, bucket.Kind, tenant, user)
	return err
}

func (m *customAuthRateLimitsModel) FindBucketForUpdateTx(ctx context.Context, tx sqlx.Session, bucket AuthRateBucket) (*AuthRateLimits, error) {
	id, tenant, user := bucket.parameters()
	var row AuthRateLimits
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf(`SELECT %s FROM %s WHERE id=$1 AND bucket_kind=$2 AND tenant_id IS NOT DISTINCT FROM $3::uuid AND user_id IS NOT DISTINCT FROM $4::uuid FOR UPDATE`, authRateLimitsRows, m.table), id, bucket.Kind, tenant, user)
	return &row, err
}

func (m *customAuthRateLimitsModel) FindDatabaseTimeTx(ctx context.Context, tx sqlx.Session) (time.Time, error) {
	var row struct {
		Now time.Time `db:"now"`
	}
	err := tx.QueryRowCtx(ctx, &row, `SELECT clock_timestamp() AS now`)
	return row.Now, err
}

func (m *customAuthRateLimitsModel) UpdateScheduleTx(ctx context.Context, tx sqlx.Session, bucket AuthRateBucket, previous, next, now time.Time) error {
	id, tenant, user := bucket.parameters()
	result, err := tx.ExecCtx(ctx, fmt.Sprintf(`UPDATE %s SET available_at=$5,updated_at=$6 WHERE id=$1 AND bucket_kind=$2 AND tenant_id IS NOT DISTINCT FROM $3::uuid AND user_id IS NOT DISTINCT FROM $4::uuid AND available_at=$7`, m.table), id, bucket.Kind, tenant, user, next, now, previous)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (m *customAuthRateLimitsModel) DeleteReclaimableAccountsTx(ctx context.Context, tx sqlx.Session, now, before time.Time, limit int64) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id IN (SELECT id FROM %s WHERE bucket_kind='password_account' AND available_at<=$1 AND updated_at<=$2 ORDER BY updated_at,id LIMIT $3 FOR UPDATE SKIP LOCKED)`, m.table, m.table), now, before, limit)
	return err
}

func (m *customAuthRateLimitsModel) CountAccountBucketsTx(ctx context.Context, tx sqlx.Session) (int64, error) {
	var count int64
	err := tx.QueryRowCtx(ctx, &count, fmt.Sprintf(`SELECT count(*) FROM %s WHERE bucket_kind='password_account'`, m.table))
	return count, err
}
