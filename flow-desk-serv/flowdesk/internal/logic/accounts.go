package logic

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/lib/pq"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const maxManagedAccounts int64 = 500
const maxManagedRoles = 53
const maxAssignedRoles = 8

var accountNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,99}$`)
var extraRolePermissions = []string{"ticket:manage", "audit:read", "run:observe", "knowledge:manage", "team:manage", "account:manage", "role:manage"}

// The final mutation fence locks only the user after resource validation has finished.
// Account writers never lock business resources; this preserves the existing lock order.
func validateCurrentIdentityTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session) error {
	current, err := identityRequired(ctx)
	if err != nil || current.Session == nil {
		return ErrUnauthorized
	}
	actor, err := s.UsersModel.FindOneForShareTx(ctx, tx, current.User.TenantId, current.User.Id)
	if err != nil {
		return err
	}
	if !actor.Enabled || actor.AuthVersion != current.User.AuthVersion {
		return ErrUnauthorized
	}
	active, err := s.SessionsModel.ExistsLiveSessionTx(ctx, tx, actor.TenantId, actor.Id, current.Session.Id)
	if err != nil {
		return err
	}
	if !active {
		return ErrUnauthorized
	}
	return nil
}

func managementActorTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, permission string) (*model.Users, error) {
	current, err := identityRequired(ctx)
	if err != nil || current.Session == nil {
		return nil, ErrUnauthorized
	}
	actor, err := s.UsersModel.FindOneForShareTx(ctx, tx, current.User.TenantId, current.User.Id)
	if err != nil {
		return nil, err
	}
	if !actor.Enabled || actor.AuthVersion != current.User.AuthVersion {
		return nil, ErrUnauthorized
	}
	active, err := s.SessionsModel.ExistsLiveSessionTx(ctx, tx, actor.TenantId, actor.Id, current.Session.Id)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrUnauthorized
	}
	allowed := permitted(actor, permission)
	if permission == "role:directory" {
		allowed = permitted(actor, "account:manage") || permitted(actor, "role:manage")
	}
	if !allowed {
		return nil, errForbidden
	}
	return actor, nil
}
func managementRead(ctx context.Context, s *svc.ServiceContext, permission string, work func(context.Context, sqlx.Session, *model.Users) error) error {
	current, err := identityRequired(ctx)
	if err != nil {
		return err
	}
	return s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, current.User.TenantId); err != nil {
			return err
		}
		actor, err := managementActorTx(ctx, s, tx, permission)
		if err != nil {
			return err
		}
		return work(ctx, tx, actor)
	})
}
func managedIdempotent[T any](ctx context.Context, s *svc.ServiceContext, permission, operation, key string, request any, status int, work func(context.Context, sqlx.Session, *model.Users) (T, error)) (T, error) {
	return idempotentAuthorized(ctx, s, operation, key, request, status, func(ctx context.Context, tx sqlx.Session) (T, error) {
		var zero T
		if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, Current(ctx).User.TenantId); err != nil {
			return zero, err
		}
		actor, err := managementActorTx(ctx, s, tx, permission)
		if err != nil {
			return zero, err
		}
		return work(ctx, tx, actor)
	}, func(ctx context.Context, tx sqlx.Session, result *T) error {
		if err := s.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, Current(ctx).User.TenantId); err != nil {
			return err
		}
		_, err := managementActorTx(ctx, s, tx, permission)
		return err
	})
}
func accountConflict(err error, code string) error {
	var violation *pq.Error
	if errors.As(err, &violation) && violation.Code == "23505" {
		return failure(409, code, "名称已被使用，请换一个名称")
	}
	return err
}
func validManagedPassword(password string) bool {
	if !utf8.ValidString(password) || len(password) < 12 || len(password) > 72 {
		return false
	}
	letter, digit := false, false
	for _, char := range password {
		letter = letter || unicode.IsLetter(char)
		digit = digit || unicode.IsDigit(char)
	}
	return letter && digit
}
func passwordFingerprint(s *svc.ServiceContext, password string) string {
	mac := hmac.New(sha256.New, []byte(s.Config.JwtSecret))
	mac.Write([]byte("flowdesk.account-password.v1\x00" + password))
	return hex.EncodeToString(mac.Sum(nil))
}
func normalizedRoleIDs(ids []string) bool {
	if len(ids) < 1 || len(ids) > maxAssignedRoles {
		return false
	}
	slices.Sort(ids)
	for index, id := range ids {
		if !validID(id) || (index > 0 && id == ids[index-1]) {
			return false
		}
	}
	return true
}
func resolveManagedRoles(roles []model.Roles, ids []string, allowDisabled bool) (string, string, error) {
	base := ""
	extra := []string{}
	rank := map[string]int{"operator": 1, "supervisor": 2, "admin": 3}
	for _, id := range ids {
		found := false
		for _, role := range roles {
			if role.Id != id {
				continue
			}
			found = true
			if !role.Enabled {
				if allowDisabled {
					break
				}
				return "", "", failure(409, "ROLE_DISABLED", "角色已禁用，请刷新选择")
			}
			if role.BuiltinRole != "" {
				if rank[role.BuiltinRole] > rank[base] {
					base = role.BuiltinRole
				}
			} else {
				var permissions []string
				if json.Unmarshal([]byte(role.Permissions), &permissions) != nil {
					return "", "", errArgument
				}
				for _, permission := range permissions {
					if !slices.Contains(extra, permission) {
						extra = append(extra, permission)
					}
				}
			}
			break
		}
		if !found {
			return "", "", errMissing
		}
	}
	if base == "" {
		return "", "", failure(400, "BASE_ROLE_REQUIRED", "请至少选择一个内置基础角色")
	}
	slices.Sort(extra)
	return base, marshal(extra), nil
}
func managedUserDataTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.Users) (types.ManagedUserData, error) {
	ids, err := s.UserRolesModel.ListRoleIDsTx(ctx, tx, row.TenantId, row.Id)
	return types.ManagedUserData{Id: row.Id, Account: row.Account, DisplayName: row.DisplayName, Role: row.Role, Enabled: row.Enabled, Revision: row.Revision, RoleIds: ids, Permissions: UserPermissions(row)}, err
}
func roleData(row *model.Roles) types.ManagedRoleData {
	permissions := []string{}
	_ = json.Unmarshal([]byte(row.Permissions), &permissions)
	if row.BuiltinRole != "" {
		permissions = Permissions(row.BuiltinRole)
	}
	return types.ManagedRoleData{Id: row.Id, Name: row.Name, BuiltinRole: row.BuiltinRole, Permissions: permissions, Enabled: row.Enabled, Revision: row.Revision}
}
func ListManagedUsers(ctx context.Context, s *svc.ServiceContext, req *types.ManagedUsersReq) (types.ManagedUserPage, error) {
	result := types.ManagedUserPage{Items: []types.ManagedUserData{}}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	err = managementRead(ctx, s, "account:manage", func(ctx context.Context, tx sqlx.Session, actor *model.Users) error {
		before, err := decodeCursor(req.Cursor, "accounts:"+actor.TenantId)
		if err != nil {
			return err
		}
		if before != "" && !validID(before) {
			return errArgument
		}
		rows, err := s.UsersModel.ListManagedTx(ctx, tx, actor.TenantId, before, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for index := range rows {
			item, err := managedUserDataTx(ctx, s, tx, &rows[index])
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if result.HasMore {
			result.NextCursor = encodeCursor("accounts:"+actor.TenantId, rows[len(rows)-1].Id)
		}
		return nil
	})
	return result, err
}
func ListManagedRoles(ctx context.Context, s *svc.ServiceContext) (types.ManagedRolePage, error) {
	result := types.ManagedRolePage{Items: []types.ManagedRoleData{}, ExtraPermissions: extraRolePermissions}
	err := managementRead(ctx, s, "role:directory", func(ctx context.Context, tx sqlx.Session, actor *model.Users) error {
		rows, err := s.RolesModel.ListByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return err
		}
		for index := range rows {
			result.Items = append(result.Items, roleData(&rows[index]))
		}
		return nil
	})
	return result, err
}
func replaceRolesTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.Users, ids []string) error {
	if err := s.UserRolesModel.DeleteByUserTx(ctx, tx, row.TenantId, row.Id); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.UserRolesModel.InsertTx(ctx, tx, &model.UserRoles{Id: orderedID(), TenantId: row.TenantId, UserId: row.Id, RoleId: id}); err != nil {
			return err
		}
	}
	return nil
}
func CreateManagedUser(ctx context.Context, s *svc.ServiceContext, req *types.CreateManagedUserReq) (types.ManagedUserData, error) {
	if _, err := requirePermission(ctx, "account:manage"); err != nil {
		return types.ManagedUserData{}, err
	}
	req.Account = strings.TrimSpace(req.Account)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !accountNamePattern.MatchString(req.Account) || req.DisplayName == "" || utf8.RuneCountInString(req.DisplayName) > 80 || !validManagedPassword(req.Password) || !normalizedRoleIDs(req.RoleIds) {
		return types.ManagedUserData{}, errArgument
	}
	fingerprint := struct {
		*types.CreateManagedUserReq
		Password string `json:"password"`
	}{req, passwordFingerprint(s, req.Password)}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return types.ManagedUserData{}, err
	}
	return managedIdempotent(ctx, s, "account:manage", "account.create", req.IdempotencyKey, fingerprint, 201, func(ctx context.Context, tx sqlx.Session, actor *model.Users) (types.ManagedUserData, error) {
		roles, err := s.RolesModel.ListByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		base, extra, err := resolveManagedRoles(roles, req.RoleIds, false)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		count, err := s.UsersModel.CountManagedTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		if count >= maxManagedAccounts {
			return types.ManagedUserData{}, failure(429, "ACCOUNT_LIMIT", "账号数量已达到上限")
		}
		row := &model.Users{Id: orderedID(), TenantId: actor.TenantId, Account: req.Account, DisplayName: req.DisplayName, PasswordHash: string(hash), Role: base, ExtraPermissions: extra, Enabled: true, Revision: 1, AuthVersion: 1}
		if err = s.UsersModel.InsertTx(ctx, tx, row); err != nil {
			return types.ManagedUserData{}, accountConflict(err, "ACCOUNT_UNAVAILABLE")
		}
		if err = replaceRolesTx(ctx, s, tx, row, req.RoleIds); err != nil {
			return types.ManagedUserData{}, err
		}
		if err = recordTeamAuditTx(ctx, s, tx, actor, "account.created", "user", row.Id, "Account created with assigned roles"); err != nil {
			return types.ManagedUserData{}, err
		}
		return managedUserDataTx(ctx, s, tx, row)
	})
}
func commitManagedUserTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, row *model.Users) error {
	if err := s.UsersModel.UpdateManagedTx(ctx, tx, row); err != nil {
		return err
	}
	if err := s.SessionsModel.UpdateRevokedUserTx(ctx, tx, row.TenantId, row.Id); err != nil {
		return err
	}
	row.Revision++
	row.AuthVersion++
	return nil
}
func UpdateManagedUser(ctx context.Context, s *svc.ServiceContext, req *types.UpdateManagedUserReq) (types.ManagedUserData, error) {
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !validID(req.UserId) || req.ExpectedRevision < 1 || req.DisplayName == "" || utf8.RuneCountInString(req.DisplayName) > 80 || !normalizedRoleIDs(req.RoleIds) {
		return types.ManagedUserData{}, errArgument
	}
	return managedIdempotent(ctx, s, "account:manage", "account.update:"+req.UserId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session, actor *model.Users) (types.ManagedUserData, error) {
		row, err := s.UsersModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.UserId)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		if row.Revision != req.ExpectedRevision {
			return types.ManagedUserData{}, failure(409, "VERSION_CONFLICT", "账号已变化，请刷新")
		}
		roles, err := s.RolesModel.ListByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		base, extra, err := resolveManagedRoles(roles, req.RoleIds, false)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		if row.Enabled && row.Role == "admin" && (!req.Enabled || base != "admin") {
			count, err := s.UsersModel.CountEnabledAdminsTx(ctx, tx, actor.TenantId)
			if err != nil {
				return types.ManagedUserData{}, err
			}
			if count <= 1 {
				return types.ManagedUserData{}, failure(409, "LAST_ADMIN_REQUIRED", "必须保留至少一个启用的内置管理员")
			}
		}
		if actor.Id == row.Id {
			return types.ManagedUserData{}, failure(409, "SELF_ACCOUNT_PROTECTED", "请由其他管理员维护当前账号")
		}
		row.DisplayName, row.Enabled, row.Role, row.ExtraPermissions = req.DisplayName, req.Enabled, base, extra
		if err = replaceRolesTx(ctx, s, tx, row, req.RoleIds); err != nil {
			return types.ManagedUserData{}, err
		}
		if err = commitManagedUserTx(ctx, s, tx, row); err != nil {
			return types.ManagedUserData{}, err
		}
		if err = recordTeamAuditTx(ctx, s, tx, actor, "account.updated", "user", row.Id, "Account and roles updated; sessions revoked"); err != nil {
			return types.ManagedUserData{}, err
		}
		return managedUserDataTx(ctx, s, tx, row)
	})
}
func ResetManagedPassword(ctx context.Context, s *svc.ServiceContext, req *types.ResetManagedPasswordReq) (types.ManagedUserData, error) {
	if _, err := requirePermission(ctx, "account:manage"); err != nil {
		return types.ManagedUserData{}, err
	}
	if !validID(req.UserId) || req.ExpectedRevision < 1 || !validManagedPassword(req.Password) {
		return types.ManagedUserData{}, errArgument
	}
	fingerprint := struct {
		*types.ResetManagedPasswordReq
		Password string `json:"password"`
	}{req, passwordFingerprint(s, req.Password)}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return types.ManagedUserData{}, err
	}
	return managedIdempotent(ctx, s, "account:manage", "account.reset:"+req.UserId, req.IdempotencyKey, fingerprint, 200, func(ctx context.Context, tx sqlx.Session, actor *model.Users) (types.ManagedUserData, error) {
		row, err := s.UsersModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.UserId)
		if err != nil {
			return types.ManagedUserData{}, err
		}
		if row.Revision != req.ExpectedRevision {
			return types.ManagedUserData{}, failure(409, "VERSION_CONFLICT", "账号已变化，请刷新")
		}
		if actor.Id == row.Id {
			return types.ManagedUserData{}, failure(409, "SELF_ACCOUNT_PROTECTED", "请由其他管理员重置当前账号密码")
		}
		row.PasswordHash = string(hash)
		if err = commitManagedUserTx(ctx, s, tx, row); err != nil {
			return types.ManagedUserData{}, err
		}
		if err = recordTeamAuditTx(ctx, s, tx, actor, "account.password_reset", "user", row.Id, "Password reset; sessions revoked"); err != nil {
			return types.ManagedUserData{}, err
		}
		return managedUserDataTx(ctx, s, tx, row)
	})
}
func validManagedRole(name string, permissions []string) bool {
	if name == "" || utf8.RuneCountInString(name) > 80 || len(permissions) > len(extraRolePermissions) {
		return false
	}
	slices.Sort(permissions)
	for index, permission := range permissions {
		if !slices.Contains(extraRolePermissions, permission) || (index > 0 && permission == permissions[index-1]) {
			return false
		}
	}
	return true
}
func CreateManagedRole(ctx context.Context, s *svc.ServiceContext, req *types.CreateManagedRoleReq) (types.ManagedRoleData, error) {
	if req.Permissions == nil {
		req.Permissions = []string{}
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validManagedRole(req.Name, req.Permissions) {
		return types.ManagedRoleData{}, errArgument
	}
	return managedIdempotent(ctx, s, "role:manage", "role.create", req.IdempotencyKey, req, 201, func(ctx context.Context, tx sqlx.Session, actor *model.Users) (types.ManagedRoleData, error) {
		rows, err := s.RolesModel.ListByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.ManagedRoleData{}, err
		}
		if len(rows) >= maxManagedRoles {
			return types.ManagedRoleData{}, failure(429, "ROLE_LIMIT", "角色数量已达到上限")
		}
		row := &model.Roles{Id: orderedID(), TenantId: actor.TenantId, Name: req.Name, Permissions: marshal(req.Permissions), Enabled: true, Revision: 1}
		if err = s.RolesModel.InsertTx(ctx, tx, row); err != nil {
			return types.ManagedRoleData{}, accountConflict(err, "ROLE_NAME_CONFLICT")
		}
		err = recordTeamAuditTx(ctx, s, tx, actor, "role.created", "role", row.Id, "Custom additive role created")
		return roleData(row), err
	})
}
func UpdateManagedRole(ctx context.Context, s *svc.ServiceContext, req *types.UpdateManagedRoleReq) (types.ManagedRoleData, error) {
	if req.Permissions == nil {
		req.Permissions = []string{}
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validID(req.RoleId) || req.ExpectedRevision < 1 || !validManagedRole(req.Name, req.Permissions) {
		return types.ManagedRoleData{}, errArgument
	}
	return managedIdempotent(ctx, s, "role:manage", "role.update:"+req.RoleId, req.IdempotencyKey, req, 200, func(ctx context.Context, tx sqlx.Session, actor *model.Users) (types.ManagedRoleData, error) {
		rows, err := s.RolesModel.ListByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.ManagedRoleData{}, err
		}
		position := slices.IndexFunc(rows, func(row model.Roles) bool { return row.Id == req.RoleId })
		if position < 0 {
			return types.ManagedRoleData{}, errMissing
		}
		row := &rows[position]
		if row.BuiltinRole != "" {
			return types.ManagedRoleData{}, failure(409, "BUILTIN_ROLE_IMMUTABLE", "内置角色不可修改")
		}
		if row.Revision != req.ExpectedRevision {
			return types.ManagedRoleData{}, failure(409, "VERSION_CONFLICT", "角色已变化，请刷新")
		}
		users, err := s.UserRolesModel.ListUserIDsTx(ctx, tx, actor.TenantId, row.Id)
		if err != nil {
			return types.ManagedRoleData{}, err
		}
		if len(users) > int(maxManagedAccounts) {
			return types.ManagedRoleData{}, failure(429, "ACCOUNT_LIMIT", "角色关联账号超过上限")
		}
		if slices.Contains(users, actor.Id) {
			return types.ManagedRoleData{}, failure(409, "SELF_ACCOUNT_PROTECTED", "请由未关联此角色的管理员维护")
		}
		row.Name, row.Permissions, row.Enabled = req.Name, marshal(req.Permissions), req.Enabled
		if err = s.RolesModel.UpdateManagedTx(ctx, tx, row); err != nil {
			return types.ManagedRoleData{}, accountConflict(err, "ROLE_NAME_CONFLICT")
		}
		row.Revision++
		for _, id := range users {
			user, err := s.UsersModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, id)
			if err != nil {
				return types.ManagedRoleData{}, err
			}
			ids, err := s.UserRolesModel.ListRoleIDsTx(ctx, tx, actor.TenantId, id)
			if err != nil {
				return types.ManagedRoleData{}, err
			}
			user.Role, user.ExtraPermissions, err = resolveManagedRoles(rows, ids, true)
			if err != nil {
				return types.ManagedRoleData{}, err
			}
			if err = commitManagedUserTx(ctx, s, tx, user); err != nil {
				return types.ManagedRoleData{}, err
			}
		}
		err = recordTeamAuditTx(ctx, s, tx, actor, "role.updated", "role", row.Id, "Role updated; affected permissions refreshed and sessions revoked")
		return roleData(row), err
	})
}
