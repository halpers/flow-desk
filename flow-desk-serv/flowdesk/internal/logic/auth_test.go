package logic

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/config"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

// Each integration test owns a disposable database, including its migration ledger.
func testService(t *testing.T) *svc.ServiceContext {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL to a development PostgreSQL administrator")
	}
	owner, err := sql.Open("postgres", raw)
	if err != nil {
		t.Fatal(err)
	}
	name := "fd_test_" + uuid.New().String()[:8]
	if _, err = owner.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`); _ = owner.Close() })
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	values := parsed.Query()
	values.Set("search_path", "business")
	parsed.RawQuery = values.Encode()
	database, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err = database.Exec("CREATE SCHEMA business"); err != nil {
		t.Fatal(err)
	}
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(database, filepath.Join("..", "..", "db", "migrations")); err != nil {
		t.Fatal(err)
	}
	service := svc.NewServiceContext(config.Config{DatabaseUrl: parsed.String(), JwtSecret: "integration-only-signing-key-32-characters"})
	ctx := context.Background()
	tenant := uuid.NewString()
	if _, err = service.TenantsModel.Insert(ctx, &model.Tenants{Id: tenant, Code: "test", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("integration-password"), bcrypt.MinCost)
	if _, err = service.UsersModel.Insert(ctx, &model.Users{Id: uuid.NewString(), TenantId: tenant, Account: "operator", PasswordHash: string(hash), DisplayName: "Operator", Role: "operator", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestRefreshReuseRevokesFamily(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	token, oldRaw, err := Login(ctx, s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, token.Data.AccessToken); err != nil {
		t.Fatal(err)
	}
	fresh, nextRaw, err := Refresh(ctx, s, oldRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, token.Data.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("rotated session must be invalid")
	}
	if _, _, err = Refresh(ctx, s, oldRaw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reused refresh accepted")
	}
	if _, err = Authenticate(ctx, s, fresh.Data.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reuse must revoke the replacement")
	}
	if _, _, err = Refresh(ctx, s, nextRaw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked family refreshed")
	}
}

func TestLogoutAndTenantConstraint(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	token, raw, err := Login(ctx, s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(ctx, s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	another := uuid.NewString()
	_, err = s.TenantsModel.Insert(ctx, &model.Tenants{Id: another, Code: "other", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	invalid := *identity.Session
	invalid.Id = uuid.NewString()
	invalid.TenantId = another
	invalid.RefreshHash = HashToken("another")
	if _, err = s.SessionsModel.Insert(ctx, &invalid); err == nil {
		t.Fatal("cross-tenant user association accepted")
	}
	if err = Logout(ctx, s, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, s, token.Data.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logout did not invalidate access")
	}
}

func TestRotationRollbackOnLaterWriteFailure(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	token, _, err := Login(ctx, s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(ctx, s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.SessionsModel.UpdateRevokedTx(ctx, tx, identity.Session.Id); err != nil {
			return err
		}
		duplicate := *identity.Session
		duplicate.Id = uuid.NewString()
		return s.SessionsModel.InsertTx(ctx, tx, &duplicate)
	})
	if err == nil {
		t.Fatal("duplicate refresh hash must fail")
	}
	if _, err = Authenticate(ctx, s, token.Data.AccessToken); err != nil {
		t.Fatal("earlier revoke escaped rolled-back transaction")
	}
}

func TestConcurrentRefreshReplayRevokesFamily(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	_, raw, err := Login(ctx, s, &types.LoginReq{Account: "operator", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	access := make(chan string, 2)
	failures := make(chan error, 2)
	for range 2 {
		group.Go(func() {
			token, _, err := Refresh(ctx, s, raw)
			if err != nil {
				failures <- err
				return
			}
			access <- token.Data.AccessToken
		})
	}
	group.Wait()
	close(access)
	close(failures)
	if len(access) != 1 || len(failures) != 1 {
		t.Fatal("concurrent replay must rotate once and reject once")
	}
	for failure := range failures {
		if !errors.Is(failure, ErrUnauthorized) {
			t.Fatal(failure)
		}
	}
	for token := range access {
		if _, err := Authenticate(ctx, s, token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("replay left an active family member")
		}
	}
}
