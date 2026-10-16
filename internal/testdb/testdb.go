// Package testdb gives tests a throwaway database migrated to the latest
// version, on the server of BALEIA_TEST_DATABASE_URL (a role allowed to
// CREATE DATABASE). Tests that need it skip without that variable.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose opens the database with database/sql
	"github.com/pressly/goose/v3"
)

// New creates the database, migrates it and drops it when the test ends.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("baleia_test_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	// Registered before anything else can fail, so a broken migration
	// leaves no database behind. Cleanups run last-in first-out: the pool
	// below is closed before this drops the database.
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		adminPool.Close()
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	sqlDB, err := goose.OpenDBWithDriver("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	err = goose.Up(sqlDB, migrationsDir())
	_ = sqlDB.Close()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// migrationsDir is db/migrations, found from this file so any package's
// tests can use it.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}
