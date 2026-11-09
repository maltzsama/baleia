package model_test

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

func identityModels() []any {
	return []any{
		&model.User{},
		&model.APIKey{},
		&model.AuthProvider{},
		&model.RefreshToken{},
		&model.PasswordResetToken{},
		&model.OIDCLoginAttempt{},
		&model.UserIDPToken{},
		&model.Team{},
		&model.TeamMember{},
		&model.Workspace{},
		&model.WorkspaceMember{},
		&model.Agent{},
		&model.AgentToken{},
		&model.AgentWorkspace{},
	}
}

func TestIdentityModelsMigrate(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	backend, err := store.BackendFromDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Run(string(backend), func(t *testing.T) {
		t.Parallel()
		models := identityModels()
		if backend == store.MySQL {
			checkMigration(t, openMySQL(t), models)
			return
		}
		checkMigration(t, openPostgres(t), models)
	})
}

// checkMigration runs AutoMigrate twice: the second pass proves the created
// schema matches the models.
func checkMigration(t *testing.T, db *gorm.DB, models []any) {
	t.Helper()
	for pass := 1; pass <= 2; pass++ {
		if err := store.AutoMigrate(db, models...); err != nil {
			t.Fatalf("AutoMigrate pass %d: %v", pass, err)
		}
	}
	for _, m := range models {
		if !db.Migrator().HasTable(m) {
			t.Fatalf("table missing for %T", m)
		}
	}
}

func openPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	adminURL := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, adminURL, store.Postgres, store.Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(admin) })

	name := "baleia_model_" + strings.ToLower(rand.Text()[:12])
	if err := admin.WithContext(ctx).Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := store.Open(ctx, u.String(), store.Postgres, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(db) })
	return db
}

func openMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	idx := strings.LastIndex(dsn, ")/")
	if idx < 0 {
		t.Fatalf("BALEIA_TEST_DATABASE_URL must be a go-sql-driver DSN: %q", dsn)
	}
	prefix, rest := dsn[:idx+2], dsn[idx+2:]
	_, params, hasParams := strings.Cut(rest, "?")
	sep := ""
	if hasParams {
		sep = "?" + params
	}
	serverDSN := prefix + sep

	ctx := context.Background()
	admin, err := store.Open(ctx, serverDSN, store.MySQL, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open admin store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(admin) })

	name := "baleia_model_" + strings.ToLower(rand.Text()[:12])
	if err := admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE IF EXISTS `" + name + "`").Error })

	db, err := store.Open(ctx, prefix+name+sep, store.MySQL, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(db) })
	return db
}
