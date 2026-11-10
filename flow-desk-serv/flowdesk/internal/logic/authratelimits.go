package logic

import (
	"context"
	"errors"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const authRateTimeout = 2 * time.Second
const authGlobalInterval = 500 * time.Millisecond
const authGlobalBurst = 10
const authPasswordInterval = time.Minute
const authPasswordBurst = 5
const authMaximumAccounts int64 = 4096
const authReclaimBatch int64 = 100
const authReclaimIdle = 15 * time.Minute

type authRatePolicy struct {
	globalKind      string
	globalInterval  time.Duration
	globalBurst     int
	account         bool
	maximumAccounts int64
}

func CheckLoginRateLimit(ctx context.Context, s *svc.ServiceContext) error {
	return consumeAuthRateLimit(ctx, s, authRatePolicy{globalKind: model.LoginGlobalBucket, globalInterval: authGlobalInterval, globalBurst: authGlobalBurst}, "", "")
}

func checkPasswordChangeRateLimit(ctx context.Context, s *svc.ServiceContext, tenant, user string) error {
	if !validID(tenant) || !validID(user) {
		return ErrUnauthorized
	}
	return consumeAuthRateLimit(ctx, s, authRatePolicy{globalKind: model.PasswordGlobalBucket, globalInterval: authGlobalInterval, globalBurst: authGlobalBurst, account: true, maximumAccounts: authMaximumAccounts}, tenant, user)
}

// GCRA stores one theoretical arrival time. Database time is read only after
// obtaining the common row lock; application clocks cannot mint extra capacity.
func nextAuthArrival(previous, now time.Time, interval time.Duration, burst int) (time.Time, time.Duration) {
	base := previous
	if base.Before(now) {
		base = now
	}
	next := base.Add(interval)
	wait := next.Sub(now.Add(time.Duration(burst) * interval))
	if wait < 0 {
		wait = 0
	}
	return next, wait
}

func authRateRejected(password bool, wait time.Duration) *BusinessError {
	code, message := "RATE_LIMITED", "登录尝试过于频繁，请稍后再试"
	if password {
		code, message = "PASSWORD_CHANGE_RATE_LIMITED", "修改密码尝试过于频繁，请稍后再试"
	}
	seconds := int64((wait + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return &BusinessError{Status: 429, Code: code, Message: message, RetryAfterSeconds: seconds}
}

func consumeAuthRateLimit(parent context.Context, s *svc.ServiceContext, policy authRatePolicy, tenant, user string) error {
	ctx, cancel := context.WithTimeout(parent, authRateTimeout)
	defer cancel()
	var denied *BusinessError
	globalKey := model.AuthRateBucket{Kind: policy.globalKind}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.AuthRateLimitsModel.InsertBucketIfAbsentTx(ctx, tx, globalKey); err != nil {
			return err
		}
		global, err := s.AuthRateLimitsModel.FindBucketForUpdateTx(ctx, tx, globalKey)
		if err != nil {
			return err
		}
		now, err := s.AuthRateLimitsModel.FindDatabaseTimeTx(ctx, tx)
		if err != nil {
			return err
		}
		globalNext, wait := nextAuthArrival(global.AvailableAt, now, policy.globalInterval, policy.globalBurst)
		if wait > 0 {
			denied = authRateRejected(policy.account, wait)
			return nil
		}
		if policy.account {
			if err := s.AuthRateLimitsModel.DeleteReclaimableAccountsTx(ctx, tx, now, now.Add(-authReclaimIdle), authReclaimBatch); err != nil {
				return err
			}
			accountKey := model.AuthRateBucket{Kind: model.PasswordAccountBucket, TenantID: tenant, UserID: user}
			account, err := s.AuthRateLimitsModel.FindBucketForUpdateTx(ctx, tx, accountKey)
			if errors.Is(err, model.ErrNotFound) {
				count, countErr := s.AuthRateLimitsModel.CountAccountBucketsTx(ctx, tx)
				if countErr != nil {
					return countErr
				}
				if count >= policy.maximumAccounts {
					denied = authRateRejected(true, time.Minute)
					return nil
				}
				if err := s.AuthRateLimitsModel.InsertBucketIfAbsentTx(ctx, tx, accountKey); err != nil {
					return err
				}
				account, err = s.AuthRateLimitsModel.FindBucketForUpdateTx(ctx, tx, accountKey)
			}
			if err != nil {
				return err
			}
			// Refresh database time after all locks, including a newly inserted key.
			now, err = s.AuthRateLimitsModel.FindDatabaseTimeTx(ctx, tx)
			if err != nil {
				return err
			}
			globalNext, wait = nextAuthArrival(global.AvailableAt, now, policy.globalInterval, policy.globalBurst)
			if wait > 0 {
				denied = authRateRejected(true, wait)
				return nil
			}
			accountNext, wait := nextAuthArrival(account.AvailableAt, now, authPasswordInterval, authPasswordBurst)
			if wait > 0 {
				denied = authRateRejected(true, wait)
				return nil
			}
			if err := s.AuthRateLimitsModel.UpdateScheduleTx(ctx, tx, accountKey, account.AvailableAt, accountNext, now); err != nil {
				return err
			}
		}
		return s.AuthRateLimitsModel.UpdateScheduleTx(ctx, tx, globalKey, global.AvailableAt, globalNext, now)
	})
	if err != nil {
		return failure(503, "AUTH_RATE_LIMIT_UNAVAILABLE", "认证服务暂时不可用，请稍后再试")
	}
	if denied != nil {
		return denied
	}
	return nil
}
