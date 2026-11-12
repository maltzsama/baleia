// Package testdb gives tests a throwaway database migrated to the latest
// version, on the server of BALEIA_TEST_DATABASE_URL (a role allowed to
// CREATE DATABASE). Tests that need it skip without that variable, except in
// CI (CI=true), where a missing database is a failure, not a silent skip.
package testdb

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/store"
	"gorm.io/gorm"
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

// New creates the database, migrates it with the GORM store migrations and
// drops it when the test ends, returning the GORM connection the application
// uses. Parallel tests wait for a free slot; see acquire.
func New(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := createDatabase(t)
	ctx := context.Background()
	if err := migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := openTestStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeDB(db) })
	return db
}

// createDatabase makes an empty database on the test server and registers its
// drop. The slot is registered first, so it is given back last: after the
// drop.
func createDatabase(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("BALEIA_TEST_DATABASE_URL not set in CI: DB-backed tests would be skipped")
		}
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	acquire(t)
	ctx := context.Background()
	// Random, not time-based: parallel tests must never pick the same name.
	name := "baleia_test_" + strings.ToLower(rand.Text()[:16])
	backend, err := store.BackendFromDSN(admin)
	if err != nil {
		t.Fatal(err)
	}
	create, drop, child := "", "", ""
	if backend == store.Postgres {
		u, err := url.Parse(admin)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		child = u.String()
		create = "CREATE DATABASE " + name
		drop = "DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"
	} else {
		adminDSN, childDSN, c, d, err := mysqlTestDSNs(admin, name)
		if err != nil {
			t.Fatal(err)
		}
		admin, child, create, drop = adminDSN, childDSN, c, d
	}
	adminDB, err := openAdmin(ctx, admin, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminDB.WithContext(ctx).Exec(create).Error; err != nil {
		_ = closeDB(adminDB)
		t.Fatal(err)
	}
	// Registered before anything else can fail, so a broken migration
	// leaves no database behind. Cleanups run last-in first-out: the test
	// store below is closed before this drops the database.
	t.Cleanup(func() {
		_ = adminDB.Exec(drop).Error
		_ = closeDB(adminDB)
	})
	return child
}

// mysqlTestDSNs splits the go-sql-driver DSN into the admin DSN (no database),
// the child DSN (the new database) and the CREATE/DROP statements.
func mysqlTestDSNs(base, name string) (adminDSN, childDSN, create, drop string, err error) {
	idx := strings.LastIndex(base, ")/")
	if idx < 0 {
		return "", "", "", "", fmt.Errorf("mysql test DSN must contain )/: %q", base)
	}
	prefix, rest := base[:idx+2], base[idx+2:]
	_, params, hasParams := strings.Cut(rest, "?")
	sep := ""
	if hasParams {
		sep = "?" + params
	}
	return prefix + sep, prefix + name + sep,
		"CREATE DATABASE `" + name + "`", "DROP DATABASE IF EXISTS `" + name + "`", nil
}

// migrate creates the schema through the GORM store registry (ADR 0020).
// Test databases are independent, so no cross-process migration lock is
// needed.
func migrate(ctx context.Context, dsn string) error {
	backend, err := store.BackendFromDSN(dsn)
	if err != nil {
		return err
	}
	gdb, err := store.Open(ctx, dsn, backend, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(gdb) }()
	reg, err := store.AllMigrations()
	if err != nil {
		return err
	}
	_, err = store.NewRunner(gdb, reg, slog.New(slog.DiscardHandler)).Up(ctx)
	return err
}

// openAdmin opens the maintenance connection used to create and drop the
// throwaway database.
func openAdmin(ctx context.Context, dsn string, backend store.Backend) (*gorm.DB, error) {
	db, err := store.Open(ctx, dsn, backend, store.Options{
		MaxOpenConns: adminConns, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		return nil, fmt.Errorf("open test database admin: %w", err)
	}
	return db, nil
}

// openTestStore opens the throwaway database's GORM connection.
func openTestStore(ctx context.Context, dsn string) (*gorm.DB, error) {
	backend, err := store.BackendFromDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, dsn, backend, store.Options{
		MaxOpenConns: poolConns, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		return nil, fmt.Errorf("open test store: %w", err)
	}
	return db, nil
}

func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return store.Close(db)
}
