// Package testdb gives tests a throwaway database migrated to the latest
// version, on the server of BALEIA_TEST_DATABASE_URL (a role allowed to
// CREATE DATABASE). Tests that need it skip without that variable, except in
// CI (CI=true), where a missing database is a failure, not a silent skip.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose opens the database with database/sql
	"github.com/pressly/goose/v3"
)

// Parallel tests share one server (#374). At most maxTests top-level tests
// of a process hold test databases at a time, each database reached through
// at most adminConns+poolConns connections: with every package of the module
// in its own process, the total stays under Postgres' default
// max_connections.
const (
	maxTests   = 4
	adminConns = 1
	poolConns  = 4
)

// slots bounds the top-level tests that hold databases at once. A test and
// its subtests share one slot however many databases they open: a test that
// opens a second database while holding the first must not wait for itself.
var (
	slots   = make(chan struct{}, maxTests)
	holders sync.Map // top-level test name -> *holder
)

type holder struct {
	mu sync.Mutex
	n  int // databases the test holds
}

// acquire takes the test's slot, if it does not hold it yet, and gives it
// back with the test's last database.
func acquire(t *testing.T) {
	t.Helper()
	root, _, _ := strings.Cut(t.Name(), "/")
	v, _ := holders.LoadOrStore(root, &holder{})
	h, _ := v.(*holder) // only *holder is ever stored
	h.mu.Lock()
	if h.n == 0 {
		slots <- struct{}{}
	}
	h.n++
	h.mu.Unlock()
	t.Cleanup(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.n--; h.n == 0 {
			<-slots
		}
	})
}

// New creates the database, migrates it and drops it when the test ends.
// Parallel tests wait for a free slot; see acquire.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("BALEIA_TEST_DATABASE_URL not set in CI: DB-backed tests would be skipped")
		}
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	// Registered first, so the slot is given back last: after the drop below.
	acquire(t)
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
	if err := migrate(ctx, adminPool, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := newPool(ctx, u.String(), poolConns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// migrationLock is the advisory lock that serializes test migrations on the
// server, across the processes of a `go test ./...` run.
const migrationLock = 0x6261_6c65_6961 // "baleia"

// migrate runs every migration through a goose Provider, which keeps no
// package state. One migration runs at a time on the server: the migrations
// create cluster-wide roles (00018 creates baleia_trino), and two databases
// migrating at once raced to create the same role.
func migrate(ctx context.Context, admin *pgxpool.Pool, dsn string) error {
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration lock connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		// Best effort: the lock also ends with the session.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock)
	}()

	// sql.Open, not goose.OpenDBWithDriver: that one sets goose's global
	// dialect, which raced between parallel tests.
	sqlDB, err := sql.Open("pgx", dsn)
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
