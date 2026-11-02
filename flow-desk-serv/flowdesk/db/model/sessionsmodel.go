package model

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ SessionsModel = (*customSessionsModel)(nil)

type (
	// SessionsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customSessionsModel.
	SessionsModel interface {
		sessionsModel
		withSession(session sqlx.Session) SessionsModel
		LockFamilyByRefreshHashTx(context.Context, sqlx.Session, string) error
		FindOneByRefreshHashForUpdateTx(context.Context, sqlx.Session, string) (*Sessions, error)
		InsertTx(context.Context, sqlx.Session, *Sessions) error
		UpdateRevokedTx(context.Context, sqlx.Session, string) error
		UpdateRevokedFamilyTx(context.Context, sqlx.Session, string) error
		ExistsLiveFamilyBySessionTx(context.Context, sqlx.Session, string, string, string) (bool, error)
	}

	customSessionsModel struct {
		*defaultSessionsModel
	}
)

// NewSessionsModel returns a model for the database table.
func NewSessionsModel(conn sqlx.SqlConn) SessionsModel {
	return &customSessionsModel{
		defaultSessionsModel: newSessionsModel(conn),
	}
}

func (m *customSessionsModel) ExistsLiveFamilyBySessionTx(ctx context.Context, tx sqlx.Session, tenant, actor, session string) (bool, error) {
	var active bool
	err := tx.QueryRowCtx(ctx, &active, fmt.Sprintf("select exists(select 1 from %s anchor join %s live on live.family_id=anchor.family_id where anchor.tenant_id=$1 and anchor.user_id=$2 and anchor.id=$3 and live.revoked_at is null and live.expires_at>now())", m.table, m.table), tenant, actor, session)
	return active, err
}

// Serialize rotation, reuse detection and logout across every token in a family.
func (m *customSessionsModel) LockFamilyByRefreshHashTx(ctx context.Context, tx sqlx.Session, hash string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("select pg_advisory_xact_lock(hashtextextended(family_id::text,0)) from %s where refresh_hash=$1", m.table), hash)
	return err
}
func (m *customSessionsModel) FindOneByRefreshHashForUpdateTx(ctx context.Context, tx sqlx.Session, hash string) (*Sessions, error) {
	var row Sessions
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where refresh_hash=$1 for update", sessionsRows, m.table), hash)
	if err == sqlx.ErrNotFound {
		return nil, ErrNotFound
	}
	return &row, err
}
func (m *customSessionsModel) InsertTx(ctx context.Context, tx sqlx.Session, row *Sessions) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customSessionsModel) UpdateRevokedTx(ctx context.Context, tx sqlx.Session, id string) error {
	result, err := tx.ExecCtx(ctx, fmt.Sprintf("update %s set revoked_at=coalesce(revoked_at,now()) where id=$1", m.table), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected != 1 {
		return sql.ErrNoRows
	}
	return err
}
func (m *customSessionsModel) UpdateRevokedFamilyTx(ctx context.Context, tx sqlx.Session, family string) error {
	_, err := tx.ExecCtx(ctx, fmt.Sprintf("update %s set revoked_at=coalesce(revoked_at,now()) where family_id=$1", m.table), family)
	return err
}

func (m *customSessionsModel) withSession(session sqlx.Session) SessionsModel {
	return NewSessionsModel(sqlx.NewSqlConnFromSession(session))
}
