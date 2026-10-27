// Package testdb gives tests a throwaway database migrated to the latest
// version, on the server of BALEIA_TEST_DATABASE_URL (a role allowed to
// CREATE DATABASE). Tests that need it skip without that variable, except in
// CI (CI=true), where a missing database is a failure, not a silent skip.
package testdb

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose opens the database with database/sql
	"github.com/pressly/goose/v3"
)

// Parallel tests share one server (#374). Each process holds at most
// maxDatabases test databases at a time, each reached through at most
// adminConns+poolConns connections: with every package of the module in its
// own process, the total stays under Postgres' default max_connections.
const (
	maxDatabases = 2
	adminConns   = 1
	poolConns    = 4
)

// slots bounds the databases this process holds at once.
var slots = make(chan struct{}, maxDatabases)

// New creates the database, migrates it and drops it when the test ends.
// Parallel tests wait for a free slot.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("BALEIA_TEST_DATABASE_URL not set in CI: DB-backed tests would be skipped")
		}
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	slots <- struct{}{}
	// Registered first, so it runs last: after the drop below.
	t.Cleanup(func() { <-slots })
	ctx := context.Background()
	adminPool, err := newPool(ctx, admin, adminConns)
	if err != nil {
		t.Fatal(err)
	}
	// Random, not time-based: parallel tests must never pick the same name.
	name := "baleia_test_" + strings.ToLower(rand.Text()[:16])
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
	if err := migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := newPool(ctx, u.String(), poolConns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// migrate runs every migration through a goose Provider, which keeps no
// package state: parallel tests migrate their databases at once.
func migrate(ctx context.Context, dsn string) error {
	sqlDB, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(migrationsDir()))
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// newPool opens a pool of at most maxConns connections.
func newPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse test database url: %w", err)
	}
	cfg.MaxConns = maxConns
	return pgxpool.NewWithConfig(ctx, cfg)
}

// migrationsDir is db/migrations, found from this file so any package's
// tests can use it.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}
