package svc

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const passwordChangeEntries = 4096
const passwordChangeIdleLifetime = 15 * time.Minute
const passwordChangeUserBurst = 5
const passwordChangeRequestsPerSecond = 2
const passwordChangeGlobalBurst = 10

type passwordChangeAccount struct {
	limiter *rate.Limiter
	seen    time.Time
}

// PasswordChangeGuard limits trusted accounts independently of IP and session.
// It is instance-local; replicas each enforce their own bounded budget.
type PasswordChangeGuard struct {
	mutex    sync.Mutex
	accounts map[string]passwordChangeAccount
	global   *rate.Limiter
}

func NewPasswordChangeGuard() *PasswordChangeGuard {
	return &PasswordChangeGuard{accounts: make(map[string]passwordChangeAccount), global: rate.NewLimiter(passwordChangeRequestsPerSecond, passwordChangeGlobalBurst)}
}

func (guard *PasswordChangeGuard) Allow(tenant, user string) bool {
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	now := time.Now()
	for key, account := range guard.accounts {
		if now.Sub(account.seen) >= passwordChangeIdleLifetime {
			delete(guard.accounts, key)
		}
	}
	key := tenant + ":" + user
	account, found := guard.accounts[key]
	if !found {
		if len(guard.accounts) >= passwordChangeEntries {
			return false
		}
		account = passwordChangeAccount{limiter: rate.NewLimiter(rate.Every(time.Minute), passwordChangeUserBurst)}
	}
	account.seen = now
	guard.accounts[key] = account
	return account.limiter.AllowN(now, 1) && guard.global.AllowN(now, 1)
}
