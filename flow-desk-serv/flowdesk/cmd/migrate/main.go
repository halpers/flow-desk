package main

import (
	"context"
	"database/sql"
	"flowdesk/db/model"
	"flowdesk/internal/config"
	"flowdesk/internal/seed"
	"flowdesk/internal/svc"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"golang.org/x/crypto/bcrypt"
	"log"
	"os"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	password := os.Getenv("DEMO_PASSWORD")
	if dsn == "" || len(password) < 12 {
		log.Fatal("DATABASE_URL and a DEMO_PASSWORD of at least 12 characters are required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("database initialization failed")
	}
	defer db.Close()
	if err = goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}
	if err = goose.Up(db, "db/migrations"); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	s := svc.NewServiceContext(config.Config{DatabaseUrl: dsn})
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	for _, code := range []string{"demo", "other"} {
		tenantID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("flowdesk.tenant."+code)).String()
		if _, err = s.TenantsModel.FindOne(ctx, tenantID); err == model.ErrNotFound {
			_, err = s.TenantsModel.Insert(ctx, &model.Tenants{Id: tenantID, Code: code, Name: code + " workspace"})
		}
		if err != nil {
			log.Fatal(err)
		}
		for _, role := range []string{"operator", "supervisor", "admin"} {
			account := code + "." + role
			if _, err = s.UsersModel.FindOneByAccount(ctx, account); err == model.ErrNotFound {
				_, err = s.UsersModel.Insert(ctx, &model.Users{Id: uuid.NewSHA1(uuid.NameSpaceDNS, []byte("flowdesk.user."+account)).String(), TenantId: tenantID, Account: account, PasswordHash: string(hash), DisplayName: account, Role: role, Enabled: true})
			}
			if err != nil {
				log.Fatal(err)
			}
		}
		if err = seed.Orders(ctx, s, tenantID); err != nil {
			log.Fatal("synthetic order seed failed")
		}
	}
	log.Print("migrations and synthetic tenant/user seeds ready; existing credentials preserved")
}
