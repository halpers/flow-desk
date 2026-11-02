package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type ApiIdempotencyModel interface {
	apiIdempotencyModel
	withSession(sqlx.Session) ApiIdempotencyModel

	LockKeyTx(context.Context, sqlx.Session, string) error
	FindOneByScopeTx(context.Context, sqlx.Session, string, string, string, string) (*ApiIdempotency, error)
	InsertTx(context.Context, sqlx.Session, *ApiIdempotency) error
}
type customApiIdempotencyModel struct{ *defaultApiIdempotencyModel }

func NewApiIdempotencyModel(conn sqlx.SqlConn) ApiIdempotencyModel {
	return &customApiIdempotencyModel{newApiIdempotencyModel(conn)}
}
func (m *customApiIdempotencyModel) withSession(tx sqlx.Session) ApiIdempotencyModel {
	return NewApiIdempotencyModel(sqlx.NewSqlConnFromSession(tx))
}

func (m *customApiIdempotencyModel) LockKeyTx(ctx context.Context, tx sqlx.Session, scope string) error {
	_, err := tx.ExecCtx(ctx, "select pg_advisory_xact_lock(hashtextextended($1,1))", scope)
	return err
}
func (m *customApiIdempotencyModel) FindOneByScopeTx(ctx context.Context, tx sqlx.Session, tenant, actor, operation, key string) (*ApiIdempotency, error) {
	var row ApiIdempotency
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and actor_user_id=$2 and operation=$3 and key_hash=$4", apiIdempotencyRows, m.table), tenant, actor, operation, key)
	return &row, err
}
func (m *customApiIdempotencyModel) InsertTx(ctx context.Context, tx sqlx.Session, row *ApiIdempotency) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
