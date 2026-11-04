package logic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maxPageSize int64 = 100
const runDeadline = 120 * time.Second
const maxTenantActiveRuns int64 = 10
const maxActorActiveRuns int64 = 2
const idempotencyLifetime = 30 * 24 * time.Hour

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{12,128}$`)

type BusinessError struct {
	Status        int
	Code, Message string
	Data          map[string]any
}

func (e *BusinessError) Error() string { return e.Code }
func failure(status int, code, message string) *BusinessError {
	return &BusinessError{Status: status, Code: code, Message: message}
}

var errArgument = failure(400, "INVALID_ARGUMENT", "输入格式不正确")
var errForbidden = failure(403, "PERMISSION_DENIED", "当前身份没有此操作权限")
var errMissing = failure(404, "RESOURCE_NOT_FOUND", "资源不存在或不可访问")
var errStale = failure(409, "LEASE_STALE", "当前执行已失效")

func validID(id string) bool { _, err := uuid.Parse(id); return err == nil && len(id) == 36 }
func identityRequired(ctx context.Context) (*Identity, error) {
	current := Current(ctx)
	if current == nil {
		return nil, ErrUnauthorized
	}
	return current, nil
}
func permitted(user *model.Users, permission string) bool {
	for _, item := range Permissions(user.Role) {
		if item == permission {
			return true
		}
	}
	return false
}

type cursorValue struct{ Scope, Position string }

func encodeCursor(scope, position string) string {
	raw, _ := json.Marshal(cursorValue{scope, position})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCursor(raw, scope string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 {
		return "", errArgument
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", errArgument
	}
	var cursor cursorValue
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Scope != scope {
		return "", errArgument
	}
	return cursor.Position, nil
}
func pageLimit(limit int64) (int64, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 1 || limit > maxPageSize {
		return 0, errArgument
	}
	return limit, nil
}
func marshal(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }

func idempotent[T any](ctx context.Context, s *svc.ServiceContext, operation, key string, request any, status int, work func(context.Context, sqlx.Session) (T, error)) (T, error) {
	return idempotentAuthorized(ctx, s, operation, key, request, status, work, func(context.Context, sqlx.Session, *T) error { return nil })
}
func idempotentAuthorized[T any](ctx context.Context, s *svc.ServiceContext, operation, key string, request any, status int, work func(context.Context, sqlx.Session) (T, error), validate func(context.Context, sqlx.Session, *T) error) (T, error) {
	var result T
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	if !idempotencyKeyPattern.MatchString(key) {
		return result, errArgument
	}
	actor := current.User
	keyHash := HashToken(key)
	requestHash := HashToken(marshal(request))
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.ApiIdempotencyModel.LockKeyTx(ctx, tx, strings.Join([]string{actor.TenantId, actor.Id, operation, keyHash}, ":")); err != nil {
			return err
		}
		previous, err := s.ApiIdempotencyModel.FindOneByScopeTx(ctx, tx, actor.TenantId, actor.Id, operation, keyHash)
		if err == nil {
			if previous.RequestHash != requestHash {
				return failure(409, "IDEMPOTENCY_CONFLICT", "相同请求键对应的内容已经改变")
			}
			if time.Now().After(previous.ExpiresAt) {
				return failure(410, "IDEMPOTENCY_EXPIRED", "请求键已过期，请重新发起")
			}
			if err = json.Unmarshal([]byte(previous.ResponseBody), &result); err != nil {
				return err
			}
			return validate(ctx, tx, &result)
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		result, err = work(ctx, tx)
		if err != nil {
			return err
		}
		if err = validate(ctx, tx, &result); err != nil {
			return err
		}
		return s.ApiIdempotencyModel.InsertTx(ctx, tx, &model.ApiIdempotency{Id: uuid.NewString(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: operation, KeyHash: keyHash, RequestHash: requestHash, ResponseStatus: int64(status), ResponseBody: marshal(result), ExpiresAt: time.Now().Add(idempotencyLifetime)})
	})
	return result, err
}
