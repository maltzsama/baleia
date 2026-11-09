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
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
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

// TestMySQLAllMigrationsExpandTypeCheck runs the real migration registry (not
// the one-migration test registry) against MySQL and MariaDB, so migration 6
// exercises its single-statement constraint replacement on both dialects and
// the expanded list is accepted while an unknown kind is still rejected.
func TestMySQLAllMigrationsExpandTypeCheck(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"BALEIA_TEST_MYSQL_URL", "BALEIA_TEST_MARIADB_URL"} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			testMySQLAllMigrations(t, env)
		})
	}
}

func testMySQLAllMigrations(t *testing.T, env string) {
	t.Helper()
	ctx := context.Background()
	db := openMySQLTestStore(t, env)
	all, err := AllMigrations()
	if err != nil {
		t.Fatal(err)
	}
	// Apply the schema up to migration 5, then install the older constraint so
	// migration 6 has to expand it instead of finding the current list.
	first, err := NewRegistry(all.All()[:5]...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunner(db, first, slog.New(slog.DiscardHandler)).Up(ctx); err != nil {
		t.Fatalf("Up to migration 5: %v", err)
	}
	if err := db.Exec("ALTER TABLE catalog_connections DROP CONSTRAINT catalog_connections_type_check").Error; err != nil {
		t.Fatalf("drop current type check: %v", err)
	}
	oldCheck := "type IN ('polaris','nessie','iceberg','lakekeeper','unity','hive','glue','postgresql','mysql','mariadb','sqlserver','oracle','sqlite','clickhouse','flightsql','trino','doris','starrocks')"
	if err := db.Exec("ALTER TABLE catalog_connections ADD CONSTRAINT catalog_connections_type_check CHECK (" + oldCheck + ")").Error; err != nil {
		t.Fatalf("install old type check: %v", err)
	}
	if _, err := NewRunner(db, all, slog.New(slog.DiscardHandler)).Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	now := time.Now().UTC()
	for _, kind := range []string{"bigquery", "system", "delta"} {
		row := model.CatalogConnection{
			ID: model.UUID(uuid.NewString()), Name: model.Text("check-" + kind),
			Type: kind, URL: "", Config: model.JSON(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Errorf("%s: the expanded type check rejected a declared kind: %v", kind, err)
		}
	}
	bad := model.CatalogConnection{
		ID: model.UUID(uuid.NewString()), Name: model.Text("check-unknown"),
		Type: "not-a-kind", URL: "", Config: model.JSON(`{}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&bad).Error; err == nil {
		t.Error("the type check accepted an unknown kind")
	}
}
