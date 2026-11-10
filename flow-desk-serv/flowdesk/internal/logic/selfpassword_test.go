package logic

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const selfPasswordReplacement = "Synthetic-new-password-456"

func selfPasswordRequest() *types.ChangeOwnPasswordReq {
	return &types.ChangeOwnPasswordReq{CurrentPassword: "integration-password", NewPassword: selfPasswordReplacement, Confirm: true}
}

func TestSelfPasswordRevokesEverySessionAndPreservesAccount(t *testing.T) {
	s := testService(t)
	owner, token, raw := managedLogin(t, s, "operator", "integration-password")
	other, otherToken, otherRaw := managedLogin(t, s, "operator", "integration-password")
	before, err := s.UsersModel.FindOne(owner, Current(owner).User.Id)
	if err != nil {
		t.Fatal(err)
	}
	var rolesBefore []string
	if err := s.Conn.TransactCtx(owner, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		rolesBefore, err = s.UserRolesModel.ListRoleIDsTx(ctx, tx, before.TenantId, before.Id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := ChangeOwnPassword(owner, s, selfPasswordRequest()); err != nil {
		t.Fatal(err)
	}
	for _, access := range []string{token, otherToken} {
		if _, err := Authenticate(owner, s, access); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("old access accepted", err)
		}
	}
	for _, refreshToken := range []string{raw, otherRaw} {
		if _, _, err := Refresh(owner, s, refreshToken); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("old refresh accepted", err)
		}
	}
	if _, _, err := Login(owner, s, &types.LoginReq{Account: "operator", Password: "integration-password"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password accepted", err)
	}
	freshContext, freshToken, _ := managedLogin(t, s, "operator", selfPasswordReplacement)
	after, err := s.UsersModel.FindOne(owner, before.Id)
	if err != nil || after.Revision != before.Revision+1 || after.AuthVersion != before.AuthVersion+1 || after.Role != before.Role || after.DisplayName != before.DisplayName || after.ExtraPermissions != before.ExtraPermissions || after.Enabled != before.Enabled {
		t.Fatal("account projection changed", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte(selfPasswordReplacement)) != nil || after.PasswordHash == selfPasswordReplacement {
		t.Fatal("new credential not bcrypt")
	}
	if err = s.Conn.TransactCtx(owner, func(ctx context.Context, tx sqlx.Session) error {
		rolesAfter, err := s.UserRolesModel.ListRoleIDsTx(ctx, tx, before.TenantId, before.Id)
		if err != nil {
			return err
		}
		if !slices.Equal(rolesBefore, rolesAfter) {
			t.Fatal("role membership changed")
		}
		for _, old := range []context.Context{owner, other} {
			live, err := s.SessionsModel.ExistsLiveFamilyBySessionTx(ctx, tx, before.TenantId, before.Id, Current(old).Session.Id)
			if err != nil {
				return err
			}
			if live {
				t.Fatal("old run family regained authorization after login")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = ChangeOwnPassword(owner, s, selfPasswordRequest()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old uncertain response retry accepted", err)
	}
	if _, err = Authenticate(freshContext, s, freshToken); err != nil {
		t.Fatal("old retry revoked new login", err)
	}
	var audits []model.AuditEvents
	if err = s.Conn.QueryRows(&audits, "select * from business.audit_events where operation='account.password_changed'"); err != nil || len(audits) != 1 {
		t.Fatal("audit count", err)
	}
	if strings.Contains(marshal(audits), selfPasswordReplacement) || strings.Contains(marshal(audits), "integration-password") || strings.Contains(marshal(audits), after.PasswordHash) {
		t.Fatal("credential in audit")
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.api_idempotency"); err != nil || count != 0 {
		t.Fatal("credential write created persisted request summary", err)
	}
}

func TestSelfPasswordValidationAndNoCredentialEffects(t *testing.T) {
	s := testService(t)
	owner, token, _ := managedLogin(t, s, "operator", "integration-password")
	before, err := s.UsersModel.FindOne(owner, Current(owner).User.Id)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		request *types.ChangeOwnPasswordReq
		code    string
	}{
		{"wrong current", &types.ChangeOwnPasswordReq{CurrentPassword: "wrong-password", NewPassword: selfPasswordReplacement, Confirm: true}, "CURRENT_PASSWORD_INVALID"},
		{"new requires digit", &types.ChangeOwnPasswordReq{CurrentPassword: "integration-password", NewPassword: "only-letters-password", Confirm: true}, "INVALID_ARGUMENT"},
		{"short", &types.ChangeOwnPasswordReq{CurrentPassword: "integration-password", NewPassword: "Short1", Confirm: true}, "INVALID_ARGUMENT"},
		{"byte bound", &types.ChangeOwnPasswordReq{CurrentPassword: "integration-password", NewPassword: strings.Repeat("中", 24) + "1", Confirm: true}, "INVALID_ARGUMENT"},
		{"no confirmation", &types.ChangeOwnPasswordReq{CurrentPassword: "integration-password", NewPassword: selfPasswordReplacement}, "INVALID_ARGUMENT"},
		{"nil", nil, "INVALID_ARGUMENT"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) { assertAccountCode(t, ChangeOwnPassword(owner, s, item.request), item.code) })
	}
	if err := ChangeOwnPassword(context.Background(), s, selfPasswordRequest()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("anonymous accepted", err)
	}
	after, err := s.UsersModel.FindOne(owner, before.Id)
	if err != nil || after.PasswordHash != before.PasswordHash || after.Revision != before.Revision || after.AuthVersion != before.AuthVersion {
		t.Fatal("validation changed credentials", err)
	}
	if _, err := Authenticate(owner, s, token); err != nil {
		t.Fatal("validation revoked session", err)
	}
	if err := ChangeOwnPassword(owner, s, selfPasswordRequest()); err != nil {
		t.Fatal(err)
	}
	fresh, _, _ := managedLogin(t, s, "operator", selfPasswordReplacement)
	assertAccountCode(t, ChangeOwnPassword(fresh, s, &types.ChangeOwnPasswordReq{CurrentPassword: selfPasswordReplacement, NewPassword: selfPasswordReplacement, Confirm: true}), "PASSWORD_UNCHANGED")
}

func TestSelfPasswordLateAuditFailureRollsBackCredentialsAndSessions(t *testing.T) {
	s := testService(t)
	owner, token, raw := managedLogin(t, s, "operator", "integration-password")
	before, err := s.UsersModel.FindOne(owner, Current(owner).User.Id)
	if err != nil {
		t.Fatal(err)
	}
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if err := ChangeOwnPassword(owner, s, selfPasswordRequest()); err == nil {
		t.Fatal("late audit failure absent")
	}
	s.AuditEventsModel = original
	after, err := s.UsersModel.FindOne(owner, before.Id)
	if err != nil || after.PasswordHash != before.PasswordHash || after.Revision != before.Revision || after.AuthVersion != before.AuthVersion {
		t.Fatal("rolled-back account mutation escaped", err)
	}
	if _, err := Authenticate(owner, s, token); err != nil {
		t.Fatal("rolled-back access revoked", err)
	}
	if _, _, err := Refresh(owner, s, raw); err != nil {
		t.Fatal("rolled-back refresh revoked", err)
	}
}

type pausedSelfPasswordTeams struct {
	model.TeamsModel
	entered chan struct{}
	release chan struct{}
}

func (barrier *pausedSelfPasswordTeams) LockAuthorizationForUpdateTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	close(barrier.entered)
	select {
	case <-barrier.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return barrier.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, tenant)
}

func TestSelfPasswordRejectsChangesAfterBcryptAndExactSessionRotation(t *testing.T) {
	for _, scenario := range []string{"admin reset", "disabled", "refresh", "logout", "new login"} {
		t.Run(scenario, func(t *testing.T) {
			s := testService(t)
			seed, _ := taskContext(t, s)
			admin := knowledgeAdminContext(t, s, seed)
			roles := managedRoles(t, s, admin)
			account := managedAccount(t, s, admin, roles["operator"])
			owner, access, raw := managedLogin(t, s, account.Account, managedTestPassword)
			bounded, cancel := context.WithTimeout(owner, 15*time.Second)
			defer cancel()
			barrier := &pausedSelfPasswordTeams{TeamsModel: s.TeamsModel, entered: make(chan struct{}), release: make(chan struct{})}
			shadow := *s
			shadow.TeamsModel = barrier
			done := make(chan error, 1)
			go func() {
				done <- ChangeOwnPassword(bounded, &shadow, &types.ChangeOwnPasswordReq{CurrentPassword: managedTestPassword, NewPassword: selfPasswordReplacement, Confirm: true})
			}()
			select {
			case <-barrier.entered:
			case <-bounded.Done():
				t.Fatal("password did not reach final fence")
			}
			var racedAccess string
			switch scenario {
			case "admin reset":
				if _, err := ResetManagedPassword(admin, s, &types.ResetManagedPasswordReq{UserId: account.Id, Password: "Admin-replacement-789", ExpectedRevision: account.Revision, IdempotencyKey: uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				request := managedUpdate(account)
				request.Enabled = false
				if _, err := UpdateManagedUser(admin, s, request); err != nil {
					t.Fatal(err)
				}
			case "refresh":
				refreshed, _, err := Refresh(owner, s, raw)
				if err != nil {
					t.Fatal(err)
				}
				racedAccess = refreshed.Data.AccessToken
			case "logout":
				if err := Logout(owner, s, raw); err != nil {
					t.Fatal(err)
				}
			case "new login":
				_, racedAccess, _ = managedLogin(t, s, account.Account, managedTestPassword)
			}
			close(barrier.release)
			err := <-done
			if scenario == "new login" {
				if err != nil {
					t.Fatal("new session should be included in revocation", err)
				}
				if _, err = Authenticate(owner, s, racedAccess); !errors.Is(err, ErrUnauthorized) {
					t.Fatal("concurrent new login survived", err)
				}
			} else {
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatal("stale credential/session was accepted", err)
				}
				if scenario == "refresh" {
					if _, err := Authenticate(owner, s, racedAccess); err != nil {
						t.Fatal("rejected stale write revoked new refresh", err)
					}
				}
			}
			if _, err := Authenticate(owner, s, access); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("old token still active", err)
			}
		})
	}
}

func TestSelfPasswordConcurrentWritersCommitOnce(t *testing.T) {
	s := testService(t)
	one, _, _ := managedLogin(t, s, "operator", "integration-password")
	two, _, _ := managedLogin(t, s, "operator", "integration-password")
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, actor := range []context.Context{one, two} {
		group.Go(func() { results <- ChangeOwnPassword(actor, s, selfPasswordRequest()) })
	}
	group.Wait()
	close(results)
	success, denied := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrUnauthorized) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatal("concurrent credential writes were not serialized", success, denied)
	}
	var audits int64
	if err := s.Conn.QueryRow(&audits, "select count(*) from business.audit_events where operation='account.password_changed'"); err != nil || audits != 1 {
		t.Fatal("duplicate password change audit", err)
	}
}

func TestSelfPasswordAccountRateLimitSurvivesNewSessions(t *testing.T) {
	s := testService(t)
	owner, _, _ := managedLogin(t, s, "operator", "integration-password")
	request := &types.ChangeOwnPasswordReq{CurrentPassword: "wrong-password", NewPassword: selfPasswordReplacement, Confirm: true}
	for range 5 {
		assertAccountCode(t, ChangeOwnPassword(owner, s, request), "CURRENT_PASSWORD_INVALID")
	}
	another, _, _ := managedLogin(t, s, "operator", "integration-password")
	assertAccountCode(t, ChangeOwnPassword(another, s, request), "PASSWORD_CHANGE_RATE_LIMITED")
	if _, err := Authenticate(owner, s, mustSelfPasswordToken(t, s)); err != nil {
		t.Fatal("rate limit should not revoke sessions", err)
	}
}

func mustSelfPasswordToken(t *testing.T, s *svc.ServiceContext) string {
	t.Helper()
	_, token, _ := managedLogin(t, s, "operator", "integration-password")
	return token
}
