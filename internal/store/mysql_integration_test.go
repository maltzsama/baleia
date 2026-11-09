package store

import (
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// openMySQLTestStore creates a throwaway database on the MySQL or MariaDB
// server named by BALEIA_TEST_DATABASE_URL (the go-sql-driver DSN); it skips
// when the configured store is PostgreSQL. The child DSN omits parseTime, the
// shape an operator is most likely to write.
func openMySQLTestStore(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	backend, err := BackendFromDSN(dsn)
	if err != nil {
		t.Fatalf("parse BALEIA_TEST_DATABASE_URL: %v", err)
	}
	if backend != MySQL {
		t.Skip("BALEIA_TEST_DATABASE_URL is not a MySQL/MariaDB DSN")
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
	dbName := "baleia_store_test_" + strings.ToLower(rand.Text()[:12])
	admin, err := Open(context.Background(), prefix+sep, MySQL, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open admin store: %v", err)
	}
	t.Cleanup(func() { _ = Close(admin) })
	if err := admin.Exec("CREATE DATABASE `" + dbName + "`").Error; err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error })
	db, err := Open(context.Background(), prefix+dbName+sep, MySQL, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })
	return db
}

func TestMySQLRunner(t *testing.T) {
	t.Parallel()
	testMySQLRunner(t)
}

// testMySQLRunner mirrors store-up, store-status and a repeated store-up
// against a MySQL/MariaDB server. The ledger stores applied_at as a time, so
// this fails if the store does not force the driver's parseTime.
func testMySQLRunner(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db := openMySQLTestStore(t)
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
