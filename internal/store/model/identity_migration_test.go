package model_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	_ "github.com/jackc/pgx/v5/stdlib" // the test creates a throwaway PostgreSQL database
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
	models := identityModels()
	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openPostgres(t), models)
	})
	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openMySQL(t, "BALEIA_TEST_MYSQL_URL"), models)
	})
	t.Run("mariadb", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openMySQL(t, "BALEIA_TEST_MARIADB_URL"), models)
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
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	ctx := context.Background()
	name := "baleia_model_" + strings.ToLower(rand.Text()[:12])
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
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

func openMySQL(t *testing.T, env string) *gorm.DB {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s not set", env)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", env, err)
	}
	if u.User == nil || u.Host == "" {
		t.Fatalf("%s must be mysql://user:pass@host:port", env)
	}
	pass, _ := u.User.Password()
	serverDSN := fmt.Sprintf("%s:%s@tcp(%s)/", u.User.Username(), pass, u.Host)

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

	db, err := store.Open(ctx, serverDSN+name, store.MySQL, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(db) })
	return db
}
