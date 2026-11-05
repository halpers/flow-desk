package logic

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const managedTestPassword = "Synthetic-account-123"

type barrierAccountVersions struct {
	model.DocumentVersionsModel
	entered chan struct{}
	release chan struct{}
}

func (m *barrierAccountVersions) FindOneByJobIdForUpdateTx(ctx context.Context, tx sqlx.Session, job string) (*model.DocumentVersions, error) {
	m.entered <- struct{}{}
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.DocumentVersionsModel.FindOneByJobIdForUpdateTx(ctx, tx, job)
}
func TestManagedKnowledgeScopeKeepsConcurrentSourceRestoreLockOrder(t *testing.T) {
	s, admin, _, version, event, store := sourceFixture(t)
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	barrier := &barrierAccountVersions{DocumentVersionsModel: s.DocumentVersionsModel, entered: make(chan struct{}, 2), release: make(chan struct{})}
	results := make(chan error, 2)
	bounded, cancel := context.WithTimeout(admin, 15*time.Second)
	defer cancel()
	for range 2 {
		shadow := *s
		shadow.DocumentVersionsModel = barrier
		shadow.SourceStore = &memorySources{objects: store.objects}
		go func() {
			_, err := RestoreOriginalSource(bounded, &shadow, &types.ArchiveSourceReq{DocumentId: version.DocumentId, VersionId: version.Id, IdempotencyKey: uuid.NewString()})
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-barrier.entered:
		case <-bounded.Done():
			t.Fatal("restore did not reach version lock")
		}
	}
	close(barrier.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent source restore", err)
		}
	}
	row, err := s.DocumentVersionsModel.FindOne(admin, version.Id)
	if err != nil || row.SourceStorage != "postgres" || row.SourceEpoch != version.SourceEpoch+1 {
		t.Fatal("restore generation mismatch", err)
	}
}

func TestManagedMigrationBackfillsExistingIdentityWithoutPasswordRotation(t *testing.T) {
	tenant, id := uuid.NewString(), uuid.NewString()
	hash, err := bcrypt.GenerateFromPassword([]byte(managedTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	s := testService(t, func(db *sql.DB) {
		if _, err := db.Exec("insert into tenants(id,code,name)values($1,'legacy','Synthetic legacy')", tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("insert into users(id,tenant_id,account,password_hash,display_name,role,enabled)values($1,$2,'legacy.admin',$3,'Legacy admin','admin',true)", id, tenant, string(hash)); err != nil {
			t.Fatal(err)
		}
	})
	ctx, _, _ := managedLogin(t, s, "legacy.admin", managedTestPassword)
	roles := managedRoles(t, s, ctx)
	if len(roles) != 3 {
		t.Fatal("builtin backfill missing")
	}
	page, err := ListManagedUsers(ctx, s, &types.ManagedUsersReq{Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("legacy directory", err)
	}
	row := page.Items[0]
	if row.Id != id || row.Revision != 1 || !slices.Equal(row.RoleIds, []string{roles["admin"]}) {
		t.Fatal("legacy role projection mismatch")
	}
	stored, err := s.UsersModel.FindOne(ctx, id)
	if err != nil || stored.PasswordHash != string(hash) || stored.AuthVersion != 1 || stored.ExtraPermissions != "[]" {
		t.Fatal("legacy credential changed", err)
	}
}

func TestManagedKnowledgePermissionRetainsTeamAndRoleACLOnReplay(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Knowledge delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	row := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, row.Account, managedTestPassword)
	base, err := CreateKnowledgeBase(manager, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic scoped policy", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	upload := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Scoped document", Filename: "synthetic.txt", Content: "Synthetic permitted policy.", IdempotencyKey: uuid.NewString()}
	version, err := UploadDocument(manager, s, upload)
	if err != nil {
		t.Fatal(err)
	}
	team := newTeam(t, s, admin)
	base, err = SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UploadDocument(manager, s, upload); err != errMissing {
		t.Fatal("upload replay escaped team scope", err)
	}
	if _, err = ListDocumentVersions(manager, s, version.DocumentId); err != errMissing {
		t.Fatal("version metadata escaped team scope", err)
	}
	preview := &types.VersionPagesReq{DocumentId: version.DocumentId, VersionId: version.Id}
	if _, err = PreviewVersionPages(manager, s, preview); err != errMissing {
		t.Fatal("preview escaped team scope", err)
	}
	if _, err = DownloadOriginalSource(manager, s, preview); err != errMissing {
		t.Fatal("original escaped team scope", err)
	}
	if _, err = WithdrawDocument(manager, s, &types.WithdrawDocumentReq{DocumentId: version.DocumentId, ExpectedVersion: 1}); err != errMissing {
		t.Fatal("withdraw escaped team scope", err)
	}
	if _, err = UploadDocumentVersion(manager, s, &types.UploadVersionReq{DocumentId: version.DocumentId, Filename: "next.txt", Content: "Must not upload", IdempotencyKey: uuid.NewString()}); err != errMissing {
		t.Fatal("new version escaped team scope", err)
	}
	if _, err = UpdateKnowledgeAcl(manager, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin", "operator"}, ExpectedRevision: base.AclRevision}); err != errMissing {
		t.Fatal("ACL update escaped team scope", err)
	}
	memberChange(t, s, admin, team, row.Id, true)
	same, err := UploadDocument(manager, s, upload)
	if err != nil || same.Id != version.Id {
		t.Fatal("permitted replay", err)
	}
	if _, err = DownloadOriginalSource(manager, s, preview); err != nil {
		t.Fatal("authorized original denied", err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: base.AclRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err = UploadDocument(manager, s, upload); err != errMissing {
		t.Fatal("replay escaped base-role ACL", err)
	}
	if _, err = DownloadOriginalSource(manager, s, preview); err != errMissing {
		t.Fatal("original escaped base-role ACL", err)
	}
}

// Refresh holds the authorization read lock while creating its replacement session.
type pausedAccountSessions struct {
	model.SessionsModel
	entered, release chan struct{}
}

func (m *pausedAccountSessions) InsertTx(ctx context.Context, tx sqlx.Session, row *model.Sessions) error {
	close(m.entered)
	<-m.release
	return m.SessionsModel.InsertTx(ctx, tx, row)
}

type announcedAccountTeams struct {
	model.TeamsModel
	entered chan struct{}
	once    sync.Once
}

func (m *announcedAccountTeams) LockAuthorizationForUpdateTx(ctx context.Context, tx sqlx.Session, tenant string) error {
	m.once.Do(func() { close(m.entered) })
	return m.TeamsModel.LockAuthorizationForUpdateTx(ctx, tx, tenant)
}

func TestManagedResetSerializesWithRefreshAndRevokesReplacement(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	row := managedAccount(t, s, admin, roles["operator"])
	_, _, raw := managedLogin(t, s, row.Account, managedTestPassword)
	refreshService := *s
	paused := &pausedAccountSessions{SessionsModel: s.SessionsModel, entered: make(chan struct{}), release: make(chan struct{})}
	refreshService.SessionsModel = paused
	type refreshed struct {
		token *types.TokenResp
		err   error
	}
	refreshResult := make(chan refreshed, 1)
	bounded, cancel := context.WithTimeout(admin, 15*time.Second)
	defer cancel()
	go func() {
		token, _, err := Refresh(bounded, &refreshService, raw)
		refreshResult <- refreshed{token, err}
	}()
	<-paused.entered
	resetService := *s
	announced := &announcedAccountTeams{TeamsModel: s.TeamsModel, entered: make(chan struct{})}
	resetService.TeamsModel = announced
	resetResult := make(chan error, 1)
	go func() {
		_, err := ResetManagedPassword(bounded, &resetService, &types.ResetManagedPasswordReq{UserId: row.Id, ExpectedRevision: 1, Password: "Synthetic-refresh-789", IdempotencyKey: uuid.NewString()})
		resetResult <- err
	}()
	<-announced.entered
	close(paused.release)
	if err := <-resetResult; err != nil {
		t.Fatal(err)
	}
	fresh := <-refreshResult
	if fresh.err != nil {
		t.Fatal(fresh.err)
	}
	if _, err := Authenticate(admin, s, fresh.token.Data.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replacement session escaped reset", err)
	}
}

func TestManagedResetRevokesAlreadyApprovedActionAcrossNewLogin(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	run, old, approval := preparedTicket(t, s, operator)
	waitApproval(t, s, operator, run, old, approval)
	lease := approveTicket(t, s, operator, run, approval)
	if _, err := ResetManagedPassword(admin, s, &types.ResetManagedPasswordReq{UserId: Current(operator).User.Id, ExpectedRevision: 1, Password: "Synthetic-action-789", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	managedLogin(t, s, "operator", "Synthetic-action-789")
	if _, err := ExecuteAction(context.Background(), s, &types.ActionReq{RunId: run.Id, ActionId: approval.ActionId}, lease.Capability); err == nil {
		t.Fatal("old approved action executed after new login")
	}
	var tickets int64
	if err := s.Conn.QueryRowCtx(admin, &tickets, "select count(*) from tickets"); err != nil || tickets != 0 {
		t.Fatal("revoked action wrote business data", err)
	}
}

func managedRoles(t *testing.T, s *svc.ServiceContext, ctx context.Context) map[string]string {
	t.Helper()
	page, err := ListManagedRoles(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, role := range page.Items {
		result[role.BuiltinRole] = role.Id
	}
	return result
}
func managedAccount(t *testing.T, s *svc.ServiceContext, ctx context.Context, roles ...string) types.ManagedUserData {
	t.Helper()
	row, err := CreateManagedUser(ctx, s, &types.CreateManagedUserReq{Account: "user." + uuid.NewString()[:8], DisplayName: "Synthetic managed user", Password: managedTestPassword, RoleIds: roles, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func managedLogin(t *testing.T, s *svc.ServiceContext, account, password string) (context.Context, string, string) {
	t.Helper()
	token, raw, err := Login(context.Background(), s, &types.LoginReq{Account: account, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(context.Background(), s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return WithIdentity(context.Background(), identity), token.Data.AccessToken, raw
}
func managedUpdate(row types.ManagedUserData) *types.UpdateManagedUserReq {
	return &types.UpdateManagedUserReq{UserId: row.Id, DisplayName: row.DisplayName, Enabled: row.Enabled, RoleIds: slices.Clone(row.RoleIds), ExpectedRevision: row.Revision, IdempotencyKey: uuid.NewString()}
}
func assertAccountCode(t *testing.T, err error, code string) {
	t.Helper()
	var business *BusinessError
	if !errors.As(err, &business) || business.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestManagedRolesProjectionRevocationAndResetReplay(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	extra, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Knowledge observers", Permissions: []string{"knowledge:manage", "run:observe"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	row := managedAccount(t, s, admin, roles["operator"], roles["supervisor"], extra.Id)
	ctx, token, raw := managedLogin(t, s, row.Account, managedTestPassword)
	if row.Role != "supervisor" || !permitted(Current(ctx).User, "knowledge:manage") || permitted(Current(ctx).User, "account:manage") {
		t.Fatal("incorrect role union or admin escalation")
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	request := &types.ResetManagedPasswordReq{UserId: row.Id, ExpectedRevision: row.Revision, Password: "Synthetic-replacement-456", IdempotencyKey: uuid.NewString()}
	reset, err := ResetManagedPassword(admin, s, request)
	if err != nil || reset.Revision != row.Revision+1 {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old access accepted", err)
	}
	if _, _, err = Refresh(ctx, s, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old refresh accepted", err)
	}
	if _, _, err = Login(ctx, s, &types.LoginReq{Account: row.Account, Password: managedTestPassword}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password accepted", err)
	}
	fresh, newToken, _ := managedLogin(t, s, row.Account, request.Password)
	replay, err := ResetManagedPassword(admin, s, request)
	if err != nil || replay.Revision != reset.Revision {
		t.Fatal("reset replay", err)
	}
	if _, err = Authenticate(ctx, s, newToken); err != nil {
		t.Fatal("replay revoked fresh login", err)
	}
	if _, err = CallTool(context.Background(), s, &types.ToolReq{ToolName: ticketSearchTool, Arguments: map[string]any{"order_no": "FD202611020002"}}, lease.Capability); err == nil {
		t.Fatal("old run family revived")
	}
	stale := &types.CreateConversationReq{Title: "Must rollback", IdempotencyKey: uuid.NewString()}
	if _, err = CreateConversation(ctx, s, stale); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale in-flight mutation accepted", err)
	}
	var count int64
	if err = s.Conn.QueryRowCtx(ctx, &count, "select count(*) from conversations where owner_user_id=$1 and title='Must rollback'", row.Id); err != nil || count != 0 {
		t.Fatal("stale mutation committed", err)
	}
	if _, err = ListManagedUsers(fresh, s, &types.ManagedUsersReq{Limit: 20}); err == nil {
		t.Fatal("custom knowledge role managed users")
	}
	changed, err := UpdateManagedRole(admin, s, &types.UpdateManagedRoleReq{RoleId: extra.Id, Name: extra.Name, Permissions: []string{"audit:read"}, Enabled: true, ExpectedRevision: extra.Revision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, newToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("role update did not revoke session", err)
	}
	newest, _, _ := managedLogin(t, s, row.Account, request.Password)
	if permitted(Current(newest).User, "knowledge:manage") || !permitted(Current(newest).User, "audit:read") {
		t.Fatal("role projection not refreshed")
	}
	changed, err = UpdateManagedRole(admin, s, &types.UpdateManagedRoleReq{RoleId: extra.Id, Name: changed.Name, Permissions: changed.Permissions, Enabled: false, ExpectedRevision: changed.Revision, IdempotencyKey: uuid.NewString()})
	if err != nil || changed.Enabled {
		t.Fatal(err)
	}
	latest, _, _ := managedLogin(t, s, row.Account, request.Password)
	if permitted(Current(latest).User, "knowledge:manage") || !permitted(Current(latest).User, "ticket:manage") {
		t.Fatal("disabled role or builtin union incorrect")
	}
	var stored model.Users
	if err = s.Conn.QueryRowCtx(ctx, &stored, "select * from users where id=$1", row.Id); err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 4 {
		t.Fatal("account projection revision", stored.Revision)
	}
	var secretLeaks int64
	if err = s.Conn.QueryRowCtx(ctx, &secretLeaks, "select count(*) from api_idempotency where response_body::text like $1", "%"+request.Password+"%"); err != nil || secretLeaks != 0 {
		t.Fatal("password in replay", err)
	}
	fingerprint := struct {
		*types.ResetManagedPasswordReq
		Password string `json:"password"`
	}{request, passwordFingerprint(s, request.Password)}
	if strings.Contains(marshal(fingerprint), request.Password) || strings.Contains(marshal(fingerprint), HashToken(request.Password)) {
		t.Fatal("password fingerprint is exposed")
	}
}

func TestManagedAccountTenantBoundariesPaginationAndValidation(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	if _, err := ListManagedRoles(operator, s); err == nil {
		t.Fatal("operator read role directory")
	}
	foreign := *Current(admin).User
	foreign.Id = uuid.NewString()
	foreign.TenantId = uuid.NewString()
	foreign.Account = "foreign.admin"
	if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign", Name: "Foreign"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignCtx, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	foreignRoles := managedRoles(t, s, foreignCtx)
	if _, err := CreateManagedUser(admin, s, &types.CreateManagedUserReq{Account: "cross.tenant", DisplayName: "No", Password: managedTestPassword, RoleIds: []string{foreignRoles["operator"]}, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("foreign role assigned")
	}
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Empty role", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal("empty role", err)
	}
	if _, err = CreateManagedUser(admin, s, &types.CreateManagedUserReq{Account: "no.base", DisplayName: "No", Password: managedTestPassword, RoleIds: []string{role.Id}, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("missing base accepted")
	}
	if _, err = CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Invalid", Permissions: []string{"admin:*"}, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("unknown permission accepted")
	}
	_, err = UpdateManagedRole(admin, s, &types.UpdateManagedRoleReq{RoleId: roles["operator"], Name: "Changed", Permissions: []string{}, Enabled: false, ExpectedRevision: 1, IdempotencyKey: uuid.NewString()})
	assertAccountCode(t, err, "BUILTIN_ROLE_IMMUTABLE")
	req := &types.CreateManagedUserReq{Account: "same.account", DisplayName: "Same", Password: managedTestPassword, RoleIds: []string{roles["operator"]}, IdempotencyKey: uuid.NewString()}
	row, err := CreateManagedUser(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	same, err := CreateManagedUser(admin, s, req)
	if err != nil || same.Id != row.Id {
		t.Fatal("create replay", err)
	}
	req.Password = "Changed-password-999"
	_, err = CreateManagedUser(admin, s, req)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	req.IdempotencyKey = uuid.NewString()
	req.RoleIds = []string{foreignRoles["operator"]}
	_, err = CreateManagedUser(foreignCtx, s, req)
	assertAccountCode(t, err, "ACCOUNT_UNAVAILABLE")
	seen := map[string]bool{}
	cursor := ""
	for range 6 {
		page, err := ListManagedUsers(admin, s, &types.ManagedUsersReq{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Id == foreign.Id || seen[item.Id] {
				t.Fatal("foreign or duplicate page")
			}
			seen[item.Id] = true
		}
		cursor = page.NextCursor
		if !page.HasMore {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatal("missing local accounts", len(seen))
	}
	page, err := ListManagedUsers(admin, s, &types.ManagedUsersReq{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ListManagedUsers(foreignCtx, s, &types.ManagedUsersReq{Limit: 1, Cursor: page.NextCursor}); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	_, err = UpdateManagedUser(foreignCtx, s, managedUpdate(row))
	if err == nil {
		t.Fatal("foreign account updated")
	}
}

func TestManagedLastAdministratorConcurrentProtection(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	second := managedAccount(t, s, admin, roles["admin"])
	other, _, _ := managedLogin(t, s, second.Account, managedTestPassword)
	original := types.ManagedUserData{Id: Current(admin).User.Id, DisplayName: "Admin", RoleIds: []string{roles["admin"]}, Revision: 1, Enabled: true}
	requests := []*types.UpdateManagedUserReq{managedUpdate(second), managedUpdate(original)}
	for _, req := range requests {
		req.Enabled = false
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, ctx := range []context.Context{admin, other} {
		go func() {
			<-start
			bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, err := UpdateManagedUser(bounded, s, requests[index])
			results <- err
		}()
	}
	close(start)
	succeeded := 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else if !errors.Is(err, ErrUnauthorized) {
			t.Fatal("unexpected concurrent error", err)
		}
	}
	if succeeded != 1 {
		t.Fatal("concurrent updates", succeeded)
	}
	var enabled int64
	if err := s.Conn.QueryRowCtx(admin, &enabled, "select count(*) from users where role='admin' and enabled"); err != nil || enabled != 1 {
		t.Fatal("lost last administrator", err)
	}
	var survivor model.Users
	if err := s.Conn.QueryRowCtx(admin, &survivor, "select * from users where role='admin' and enabled"); err != nil {
		t.Fatal(err)
	}
	password := managedTestPassword
	if survivor.Account == "admin" {
		password = "integration-password"
	}
	ctx, _, _ := managedLogin(t, s, survivor.Account, password)
	self := &types.UpdateManagedUserReq{UserId: survivor.Id, DisplayName: survivor.DisplayName, Enabled: false, RoleIds: []string{roles["admin"]}, ExpectedRevision: survivor.Revision, IdempotencyKey: uuid.NewString()}
	_, err := UpdateManagedUser(ctx, s, self)
	assertAccountCode(t, err, "LAST_ADMIN_REQUIRED")
	self.Enabled = true
	self.IdempotencyKey = uuid.NewString()
	_, err = UpdateManagedUser(ctx, s, self)
	assertAccountCode(t, err, "SELF_ACCOUNT_PROTECTED")
}

type failingAccountAudit struct{ model.AuditEventsModel }

func (m failingAccountAudit) InsertTx(context.Context, sqlx.Session, *model.AuditEvents) error {
	return errors.New("synthetic late audit failure")
}
func TestManagedAccountLateFailureRollsBackBindingsAndSessions(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	row := managedAccount(t, s, admin, roles["operator"])
	_, token, _ := managedLogin(t, s, row.Account, managedTestPassword)
	req := managedUpdate(row)
	req.Enabled = false
	req.RoleIds = []string{roles["supervisor"]}
	original := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{original}
	if _, err := UpdateManagedUser(admin, s, req); err == nil {
		t.Fatal("late failure missing")
	}
	s.AuditEventsModel = original
	if _, err := Authenticate(admin, s, token); err != nil {
		t.Fatal("rollback revoked session", err)
	}
	page, err := ListManagedUsers(admin, s, &types.ManagedUsersReq{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, found := range page.Items {
		if found.Id == row.Id && (!found.Enabled || found.Revision != 1 || !slices.Equal(found.RoleIds, row.RoleIds)) {
			t.Fatal("partial account update")
		}
	}
	updated, err := UpdateManagedUser(admin, s, req)
	if err != nil || updated.Enabled {
		t.Fatal("rollback left idempotency or partial role", err)
	}
}

type pausedLoginUsers struct {
	model.UsersModel
	account string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *pausedLoginUsers) FindOneByAccount(ctx context.Context, account string) (*model.Users, error) {
	row, err := m.UsersModel.FindOneByAccount(ctx, account)
	if account == m.account {
		m.once.Do(func() { close(m.entered); <-m.release })
	}
	return row, err
}
func TestManagedPasswordResetFencesLoginAfterOldPasswordLookup(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	row := managedAccount(t, s, admin, roles["operator"])
	shadow := *s
	paused := &pausedLoginUsers{UsersModel: s.UsersModel, account: row.Account, entered: make(chan struct{}), release: make(chan struct{})}
	shadow.UsersModel = paused
	result := make(chan error, 1)
	go func() {
		_, _, err := Login(context.Background(), &shadow, &types.LoginReq{Account: row.Account, Password: managedTestPassword})
		result <- err
	}()
	<-paused.entered
	if _, err := ResetManagedPassword(admin, s, &types.ResetManagedPasswordReq{UserId: row.Id, ExpectedRevision: 1, Password: "Synthetic-newpass-789", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	close(paused.release)
	if err := <-result; !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password login crossed reset", err)
	}
	managedLogin(t, s, row.Account, "Synthetic-newpass-789")
}

func TestManagedRoleSelfProtectionAndDisabledAccount(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Delegated management", Permissions: []string{"role:manage", "account:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	row := managedAccount(t, s, admin, roles["operator"], role.Id)
	ctx, token, raw := managedLogin(t, s, row.Account, managedTestPassword)
	_, err = UpdateManagedRole(ctx, s, &types.UpdateManagedRoleReq{RoleId: role.Id, Name: role.Name, Permissions: role.Permissions, Enabled: false, ExpectedRevision: 1, IdempotencyKey: uuid.NewString()})
	assertAccountCode(t, err, "SELF_ACCOUNT_PROTECTED")
	if _, err = ListManagedUsers(ctx, s, &types.ManagedUsersReq{Limit: 10}); err != nil {
		t.Fatal("custom account permission denied", err)
	}
	req := managedUpdate(row)
	req.Enabled = false
	row, err = UpdateManagedUser(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled token valid", err)
	}
	if _, _, err = Refresh(ctx, s, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled refresh valid", err)
	}
	if _, _, err = Login(ctx, s, &types.LoginReq{Account: row.Account, Password: managedTestPassword}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled login valid", err)
	}
	if _, err = CreateManagedRole(ctx, s, &types.CreateManagedRoleReq{Name: "Stale actor", Permissions: []string{}, IdempotencyKey: uuid.NewString()}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale manager wrote", err)
	}
	req = managedUpdate(row)
	req.Enabled = true
	if _, err = UpdateManagedUser(admin, s, req); err != nil {
		t.Fatal(err)
	}
	managedLogin(t, s, row.Account, managedTestPassword)
}
