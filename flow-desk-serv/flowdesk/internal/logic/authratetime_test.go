package logic

import (
	"context"
	"testing"
	"time"

	"flowdesk/db/model"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type rewindingAuthRateClock struct {
	model.AuthRateLimitsModel
	reads int
}

func (clock *rewindingAuthRateClock) FindDatabaseTimeTx(ctx context.Context, tx sqlx.Session) (time.Time, error) {
	now, err := clock.AuthRateLimitsModel.FindDatabaseTimeTx(ctx, tx)
	clock.reads++
	if clock.reads == 2 {
		now = now.Add(-2 * time.Second)
	}
	return now, err
}

func TestAuthRateRechecksGlobalBudgetAfterAccountLockAndClockRewind(t *testing.T) {
	s := testService(t)
	owner, _, _ := managedLogin(t, s, "operator", "integration-password")
	tenant, user := Current(owner).User.TenantId, Current(owner).User.Id
	if err := checkPasswordChangeRateLimit(owner, s, tenant, user); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.ExecCtx(owner, `UPDATE auth_rate_limits SET available_at=clock_timestamp()+interval '4 seconds' WHERE id='password_global'`); err != nil {
		t.Fatal(err)
	}
	var before, after string
	query := `SELECT string_agg(id||'='||available_at::text,',' ORDER BY id) FROM auth_rate_limits`
	if err := s.Conn.QueryRowCtx(owner, &before, query); err != nil {
		t.Fatal(err)
	}
	s.AuthRateLimitsModel = &rewindingAuthRateClock{AuthRateLimitsModel: s.AuthRateLimitsModel}
	if err := checkPasswordChangeRateLimit(owner, s, tenant, user); authRateCode(err) != "PASSWORD_CHANGE_RATE_LIMITED" {
		t.Fatal("second database time bypassed global budget", err)
	}
	if err := s.Conn.QueryRowCtx(owner, &after, query); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("clock rewind partially consumed account or global budget")
	}
}
