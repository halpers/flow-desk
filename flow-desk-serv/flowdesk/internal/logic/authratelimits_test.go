package logic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func authRateCode(err error) string {
	var problem *BusinessError
	if errors.As(err, &problem) {
		return problem.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestAuthRateSharedConnectionsConcurrentAndRestart(t *testing.T) {
	s := testService(t)
	second := svc.NewServiceContext(s.Config)
	policy := authRatePolicy{globalKind: model.LoginGlobalBucket, globalInterval: time.Hour, globalBurst: 5}
	var workers sync.WaitGroup
	results := make(chan error, 20)
	start := make(chan struct{})
	for index := range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			instance := s
			if index%2 != 0 {
				instance = second
			}
			results <- consumeAuthRateLimit(context.Background(), instance, policy, "", "")
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if authRateCode(err) == "RATE_LIMITED" {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 5 || rejected != 15 {
		t.Fatal("shared budget overspent", accepted, rejected)
	}
	restarted := svc.NewServiceContext(s.Config)
	if err := consumeAuthRateLimit(context.Background(), restarted, policy, "", ""); authRateCode(err) != "RATE_LIMITED" {
		t.Fatal("restart refilled budget", err)
	}
	if _, err := s.Conn.ExecCtx(context.Background(), `UPDATE auth_rate_limits SET available_at=clock_timestamp()-interval '1 second' WHERE id='login_global'`); err != nil {
		t.Fatal(err)
	}
	if err := consumeAuthRateLimit(context.Background(), restarted, policy, "", ""); err != nil {
		t.Fatal("database time did not refill", err)
	}
}

func rateTestUser(t *testing.T, s *svc.ServiceContext, tenant string) string {
	t.Helper()
	user := &model.Users{Id: uuid.NewString(), TenantId: tenant, Account: "rate." + uuid.NewString(), DisplayName: "Synthetic rate user", PasswordHash: "synthetic-unused-hash", Role: "operator", Enabled: true}
	if _, err := s.UsersModel.Insert(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	return user.Id
}

func TestAuthRateTrustedAccountIsolationAndGlobalSeparation(t *testing.T) {
	s := testService(t)
	owner, _, _ := managedLogin(t, s, "operator", "integration-password")
	tenant, user := Current(owner).User.TenantId, Current(owner).User.Id
	for range authPasswordBurst {
		if err := checkPasswordChangeRateLimit(owner, s, tenant, user); err != nil {
			t.Fatal(err)
		}
	}
	restarted := svc.NewServiceContext(s.Config)
	err := checkPasswordChangeRateLimit(owner, restarted, tenant, user)
	var problem *BusinessError
	if !errors.As(err, &problem) || problem.Code != "PASSWORD_CHANGE_RATE_LIMITED" || problem.RetryAfterSeconds < 1 || problem.RetryAfterSeconds > 60 {
		t.Fatal("account debt lost", err)
	}
	other := rateTestUser(t, s, tenant)
	if err := checkPasswordChangeRateLimit(owner, restarted, tenant, other); err != nil {
		t.Fatal("other account blocked", err)
	}
	if err := CheckLoginRateLimit(owner, restarted); err != nil {
		t.Fatal("password budget consumed login", err)
	}
	wrongTenant := uuid.NewString()
	_, err = s.TenantsModel.Insert(owner, &model.Tenants{Id: wrongTenant, Code: "rate-other", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPasswordChangeRateLimit(owner, s, wrongTenant, user); authRateCode(err) != "AUTH_RATE_LIMIT_UNAVAILABLE" {
		t.Fatal("cross-tenant association accepted", err)
	}
}

func TestAuthRateLaterWriteFailureRollsBackBothBucketsAndPassword(t *testing.T) {
	s := testService(t)
	owner, token, _ := managedLogin(t, s, "operator", "integration-password")
	tenant, user := Current(owner).User.TenantId, Current(owner).User.Id
	if err := checkPasswordChangeRateLimit(owner, s, tenant, user); err != nil {
		t.Fatal(err)
	}
	var before, after string
	query := `SELECT string_agg(id||'='||available_at::text,',' ORDER BY id) FROM auth_rate_limits`
	if err := s.Conn.QueryRowCtx(owner, &before, query); err != nil {
		t.Fatal(err)
	}
	_, err := s.Conn.ExecCtx(owner, `CREATE FUNCTION reject_rate_global() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.bucket_kind='password_global' THEN RAISE EXCEPTION 'synthetic rate failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_rate_global BEFORE UPDATE ON auth_rate_limits FOR EACH ROW EXECUTE FUNCTION reject_rate_global()`)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountCode(t, ChangeOwnPassword(owner, s, selfPasswordRequest()), "AUTH_RATE_LIMIT_UNAVAILABLE")
	if err := s.Conn.QueryRowCtx(owner, &after, query); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("partial bucket debit escaped rollback")
	}
	if _, err := Authenticate(owner, s, token); err != nil {
		t.Fatal("failed limiter revoked session", err)
	}
	if _, _, err := Login(owner, s, &types.LoginReq{Account: "operator", Password: "integration-password"}); err != nil {
		t.Fatal("failed limiter changed password", err)
	}
}

func TestAuthRateCapacityReclaimsOnlyIdleFullyRefilledAccounts(t *testing.T) {
	s := testService(t)
	owner, _, _ := managedLogin(t, s, "operator", "integration-password")
	tenant := Current(owner).User.TenantId
	users := []string{Current(owner).User.Id, rateTestUser(t, s, tenant), rateTestUser(t, s, tenant)}
	policy := authRatePolicy{globalKind: model.PasswordGlobalBucket, globalInterval: time.Millisecond, globalBurst: 100, account: true, maximumAccounts: 2}
	for _, user := range users[:2] {
		if err := consumeAuthRateLimit(owner, s, policy, tenant, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := consumeAuthRateLimit(owner, s, policy, tenant, users[2]); authRateCode(err) != "PASSWORD_CHANGE_RATE_LIMITED" {
		t.Fatal("account cap bypassed", err)
	}
	// Old activity alone is insufficient: the second account still owes capacity.
	_, err := s.Conn.ExecCtx(owner, `UPDATE auth_rate_limits SET updated_at=clock_timestamp()-interval '16 minutes',available_at=CASE WHEN user_id=$1 THEN clock_timestamp()-interval '1 second' ELSE clock_timestamp()+interval '5 minutes' END WHERE bucket_kind='password_account'`, users[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := consumeAuthRateLimit(owner, s, policy, tenant, users[2]); err != nil {
		t.Fatal("safe idle slot was not reclaimed", err)
	}
	var count int64
	if err := s.Conn.QueryRowCtx(owner, &count, `SELECT count(*) FROM auth_rate_limits WHERE bucket_kind='password_account'`); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("unbounded account state", count)
	}
	if err := consumeAuthRateLimit(owner, s, policy, tenant, users[1]); authRateCode(err) != "PASSWORD_CHANGE_RATE_LIMITED" {
		t.Fatal("live debt was evicted", err)
	}
}

func TestAuthRateLockDeadlineAndUnavailableDatabaseFailClosed(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := CheckLoginRateLimit(ctx, s); err != nil {
		t.Fatal(err)
	}
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
			if _, err := s.AuthRateLimitsModel.FindBucketForUpdateTx(ctx, tx, model.AuthRateBucket{Kind: model.LoginGlobalBucket}); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("lock not acquired")
	}
	short, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	err := CheckLoginRateLimit(short, s)
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if authRateCode(err) != "AUTH_RATE_LIMIT_UNAVAILABLE" {
		t.Fatal("blocked database allowed login", err)
	}
	if err := CheckLoginRateLimit(ctx, s); err != nil {
		t.Fatal("lock failure consumed budget", err)
	}
	broken := *s
	broken.AuthRateLimitsModel = failedAuthRateModel{s.AuthRateLimitsModel}
	if err := CheckLoginRateLimit(ctx, &broken); authRateCode(err) != "AUTH_RATE_LIMIT_UNAVAILABLE" {
		t.Fatal("failure opened limiter", err)
	}
}

type failedAuthRateModel struct{ model.AuthRateLimitsModel }

func (failedAuthRateModel) FindDatabaseTimeTx(context.Context, sqlx.Session) (time.Time, error) {
	return time.Time{}, fmt.Errorf("synthetic database unavailable")
}
