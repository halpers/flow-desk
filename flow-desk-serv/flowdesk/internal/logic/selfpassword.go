package logic

import (
	"context"
	"errors"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

// ChangeOwnPassword never stores an idempotency fingerprint of either password.
// After an uncertain response callers sign in again rather than retrying a write
// with a deliberately revoked session.
func ChangeOwnPassword(ctx context.Context, s *svc.ServiceContext, req *types.ChangeOwnPasswordReq) error {
	identity, err := identityRequired(ctx)
	if err != nil || identity.User == nil || identity.Session == nil {
		return ErrUnauthorized
	}
	if req == nil || !req.Confirm || !utf8.ValidString(req.CurrentPassword) || len(req.CurrentPassword) < 1 || len(req.CurrentPassword) > 128 || !validManagedPassword(req.NewPassword) {
		return errArgument
	}
	var observed *model.Users
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, identity.User.TenantId); err != nil {
			return err
		}
		if err := validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		var err error
		observed, err = s.UsersModel.FindOneTx(ctx, tx, identity.User.Id)
		return err
	})
	if err != nil {
		return err
	}
	if err := checkPasswordChangeRateLimit(ctx, s, observed.TenantId, observed.Id); err != nil {
		return err
	}
	// CPU work runs without database locks. The original credential and exact
	// live session are checked again after taking the authorization write lock.
	if bcrypt.CompareHashAndPassword([]byte(observed.PasswordHash), []byte(req.CurrentPassword)) != nil {
		return failure(400, "CURRENT_PASSWORD_INVALID", "当前密码不正确")
	}
	if bcrypt.CompareHashAndPassword([]byte(observed.PasswordHash), []byte(req.NewPassword)) == nil {
		return failure(400, "PASSWORD_UNCHANGED", "新密码不能与当前密码相同")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, observed.TenantId); err != nil {
			return err
		}
		fresh, err := s.UsersModel.FindOneForUpdateTx(ctx, tx, observed.TenantId, observed.Id)
		if errors.Is(err, model.ErrNotFound) {
			return ErrUnauthorized
		}
		if err != nil {
			return err
		}
		if !fresh.Enabled || fresh.AuthVersion != observed.AuthVersion || fresh.AuthVersion != identity.User.AuthVersion || fresh.PasswordHash != observed.PasswordHash {
			return ErrUnauthorized
		}
		active, err := s.SessionsModel.ExistsLiveSessionTx(ctx, tx, fresh.TenantId, fresh.Id, identity.Session.Id)
		if err != nil {
			return err
		}
		if !active {
			return ErrUnauthorized
		}
		fresh.PasswordHash = string(hash)
		if err = commitManagedUserTx(ctx, s, tx, fresh); err != nil {
			return err
		}
		return recordTeamAuditTx(ctx, s, tx, fresh, "account.password_changed", "user", fresh.Id, "Password changed by account owner; all sessions revoked")
	})
}
