package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// openMySQLTestStore creates a throwaway database on the MySQL or MariaDB
// server named by env and opens the store with a DSN that omits parseTime, the
// shape an operator is most likely to write. The env contract is the same as
// the rest of the repository: mysql://user:pass@host:port.
func openMySQLTestStore(t *testing.T, env string) *gorm.DB {
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

	admin, err := Open(context.Background(), serverDSN, MySQL, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open admin store: %v", err)
	}
	t.Cleanup(func() { _ = Close(admin) })

	dbName := "baleia_store_test_" + strings.ToLower(rand.Text()[:12])
	if err := admin.Exec("CREATE DATABASE `" + dbName + "`").Error; err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error })

	db, err := Open(context.Background(), serverDSN+dbName, MySQL, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })
	return db
}

func TestMySQLRunner(t *testing.T) {
	t.Parallel()
	testMySQLRunner(t, "BALEIA_TEST_MYSQL_URL")
}

func TestMariaDBRunner(t *testing.T) {
	t.Parallel()
	testMySQLRunner(t, "BALEIA_TEST_MARIADB_URL")
}

// testMySQLRunner mirrors store-up, store-status and a repeated store-up
// against a MySQL/MariaDB server. The ledger stores applied_at as a time, so
// this fails if the store does not force the driver's parseTime.
func testMySQLRunner(t *testing.T, env string) {
	t.Helper()
	ctx := context.Background()
	db := openMySQLTestStore(t, env)
	runner := NewRunner(db, testRegistry(t), slog.New(slog.DiscardHandler))

	applied, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Fatalf("applied = %+v, want migration 1", applied)
	}
	if !db.Migrator().HasTable(&runnerTestRow{}) {
		t.Fatal("migration table was not created")
	}

	statuses, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Applied || statuses[0].AppliedAt.IsZero() {
		t.Fatalf("status = %+v, want applied migration 1 with a timestamp", statuses)
	}

	again, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Up applied %+v, want none", again)
	}

	rolledBack, err := runner.Down(ctx)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if rolledBack == nil || rolledBack.Version != 1 {
		t.Fatalf("rolled back = %+v, want migration 1", rolledBack)
	}
	if db.Migrator().HasTable(&runnerTestRow{}) {
		t.Fatal("migration table survived the rollback")
	}
}
