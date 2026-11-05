package logic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const AccessLifetime = 15 * time.Minute
const RefreshLifetime = 8 * time.Hour

var ErrUnauthorized = errors.New("invalid or expired credentials")

type Identity struct {
	User    *model.Users
	Session *model.Sessions
}
type identityKey struct{}

func WithIdentity(ctx context.Context, identity *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}
func Current(ctx context.Context) *Identity {
	identity, _ := ctx.Value(identityKey{}).(*Identity)
	return identity
}
func HashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func newRefresh() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func issue(ctx context.Context, s *svc.ServiceContext, user *model.Users, session *model.Sessions, raw string) (*types.TokenResp, string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "flowdesk", "aud": "flowdesk-web", "sub": user.Id, "sid": session.Id, "tenant_id": user.TenantId, "exp": now.Add(AccessLifetime).Unix(), "iat": now.Unix()})
	signed, err := token.SignedString([]byte(s.Config.JwtSecret))
	if err != nil {
		return nil, "", err
	}
	return &types.TokenResp{Code: "OK", Data: types.TokenData{AccessToken: signed, ExpiresIn: int64(AccessLifetime.Seconds())}}, raw, nil
}

func Login(ctx context.Context, s *svc.ServiceContext, req *types.LoginReq) (*types.TokenResp, string, error) {
	if len(req.Account) > 100 || len(req.Password) > 128 {
		return nil, "", ErrUnauthorized
	}
	user, err := s.UsersModel.FindOneByAccount(ctx, req.Account)
	if err != nil || !user.Enabled {
		return nil, "", ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		return nil, "", ErrUnauthorized
	}
	raw, err := newRefresh()
	if err != nil {
		return nil, "", err
	}
	session := &model.Sessions{Id: uuid.NewString(), TenantId: user.TenantId, UserId: user.Id, RefreshHash: HashToken(raw), FamilyId: uuid.NewString(), ExpiresAt: time.Now().Add(RefreshLifetime)}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, user.TenantId); err != nil {
			return err
		}
		fresh, err := s.UsersModel.FindOneTx(ctx, tx, user.Id)
		if err != nil || !fresh.Enabled || fresh.AuthVersion != user.AuthVersion || fresh.PasswordHash != user.PasswordHash {
			return ErrUnauthorized
		}
		return s.SessionsModel.InsertTx(ctx, tx, session)
	})
	if err != nil {
		return nil, "", err
	}
	return issue(ctx, s, user, session, raw)
}

func Refresh(ctx context.Context, s *svc.ServiceContext, raw string) (*types.TokenResp, string, error) {
	if len(raw) != 64 {
		return nil, "", ErrUnauthorized
	}
	var user *model.Users
	var replacement *model.Sessions
	var nextRaw string
	denied := false
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		anchor, err := s.SessionsModel.FindOneByRefreshHashTx(ctx, tx, HashToken(raw))
		if err != nil {
			return ErrUnauthorized
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, anchor.TenantId); err != nil {
			return err
		}
		if err := s.SessionsModel.LockFamilyByRefreshHashTx(ctx, tx, HashToken(raw)); err != nil {
			return err
		}
		previous, err := s.SessionsModel.FindOneByRefreshHashForUpdateTx(ctx, tx, HashToken(raw))
		if err != nil {
			return ErrUnauthorized
		}
		if previous.RevokedAt.Valid {
			denied = true
			return s.SessionsModel.UpdateRevokedFamilyTx(ctx, tx, previous.FamilyId)
		}
		if time.Now().After(previous.ExpiresAt) {
			return ErrUnauthorized
		}
		user, err = s.UsersModel.FindOneTx(ctx, tx, previous.UserId)
		if err != nil || !user.Enabled {
			return ErrUnauthorized
		}
		nextRaw, err = newRefresh()
		if err != nil {
			return err
		}
		replacement = &model.Sessions{Id: uuid.NewString(), TenantId: user.TenantId, UserId: user.Id, RefreshHash: HashToken(nextRaw), FamilyId: previous.FamilyId, ExpiresAt: previous.ExpiresAt}
		if err = s.SessionsModel.UpdateRevokedTx(ctx, tx, previous.Id); err != nil {
			return err
		}
		return s.SessionsModel.InsertTx(ctx, tx, replacement)
	})
	if err != nil {
		return nil, "", err
	}
	if denied {
		return nil, "", ErrUnauthorized
	}
	return issue(ctx, s, user, replacement, nextRaw)
}

func Logout(ctx context.Context, s *svc.ServiceContext, raw string) error {
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		anchor, err := s.SessionsModel.FindOneByRefreshHashTx(ctx, tx, HashToken(raw))
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, anchor.TenantId); err != nil {
			return err
		}
		if err := s.SessionsModel.LockFamilyByRefreshHashTx(ctx, tx, HashToken(raw)); err != nil {
			return err
		}
		session, err := s.SessionsModel.FindOneByRefreshHashForUpdateTx(ctx, tx, HashToken(raw))
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.SessionsModel.UpdateRevokedFamilyTx(ctx, tx, session.FamilyId)
	})
}

func Authenticate(ctx context.Context, s *svc.ServiceContext, raw string) (*Identity, error) {
	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, ErrUnauthorized
		}
		return []byte(s.Config.JwtSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return nil, ErrUnauthorized
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !claims.VerifyIssuer("flowdesk", true) || !claims.VerifyAudience("flowdesk-web", true) {
		return nil, ErrUnauthorized
	}
	sessionID, _ := claims["sid"].(string)
	session, err := s.SessionsModel.FindOne(ctx, sessionID)
	if err != nil || session.RevokedAt.Valid || time.Now().After(session.ExpiresAt) {
		return nil, ErrUnauthorized
	}
	user, err := s.UsersModel.FindOne(ctx, session.UserId)
	if err != nil || !user.Enabled {
		return nil, ErrUnauthorized
	}
	if claims["sub"] != user.Id || claims["tenant_id"] != user.TenantId || session.TenantId != user.TenantId {
		return nil, ErrUnauthorized
	}
	return &Identity{User: user, Session: session}, nil
}

func Permissions(role string) []string {
	permissions := []string{"conversation:use", "order:read", "ticket:read", "ticket:create", "ticket:note"}
	if role == "supervisor" || role == "admin" {
		permissions = append(permissions, "ticket:manage", "audit:read", "run:observe")
	}
	if role == "admin" {
		permissions = append(permissions, "knowledge:manage", "team:manage", "account:manage", "role:manage")
	}
	return permissions
}

func UserPermissions(user *model.Users) []string {
	result := Permissions(user.Role)
	var extra []string
	if json.Unmarshal([]byte(user.ExtraPermissions), &extra) == nil {
		for _, permission := range extra {
			if slices.Contains(extraRolePermissions, permission) && !slices.Contains(result, permission) {
				result = append(result, permission)
			}
		}
	}
	return result
}
