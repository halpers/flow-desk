package model

import (
	"context"
	"errors"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var ErrAuthorizationBusy = errors.New("team authorization is changing")

// Never wait for an exclusive authorizer while holding resource row locks.
// A failed try rolls back the caller, allowing membership revocation to finish.
func lockAuthorizationForShareTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	var acquired bool
	if err := tx.QueryRowCtx(ctx, &acquired, "select pg_try_advisory_xact_lock_shared(hashtextextended($1,64))", tenant); err != nil {
		return err
	}
	if !acquired {
		return ErrAuthorizationBusy
	}
	return nil
}

// These predicates are used only with trusted Go user context. The outer table
// must use its real name (not an alias) when using teamAccessPredicate.
const teamAccessPredicate = `(team_id is null or $%[1]d='admin' or exists
 (select 1 from business.team_members tm where tm.tenant_id=%[2]s.tenant_id
 and tm.team_id=%[2]s.team_id and tm.user_id::text=$%[3]d))`
