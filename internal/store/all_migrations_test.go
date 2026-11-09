package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	_ "github.com/jackc/pgx/v5/stdlib" // the test creates a throwaway PostgreSQL database
)

func TestAllMigrationsUpCreatesSchema(t *testing.T) {
	t.Parallel()
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
	name := "baleia_migrate_" + strings.ToLower(rand.Text()[:12])
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
	db, err := Open(ctx, u.String(), Postgres, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })

	reg, err := AllMigrations()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(db, reg, slog.New(slog.DiscardHandler))
	applied, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	want := reg.All()
	if len(applied) != len(want) {
		t.Fatalf("applied = %+v, want %d migrations", applied, len(want))
	}
	for i, m := range want {
		if applied[i].Version != m.Version {
			t.Fatalf("applied[%d] = %d, want %d", i, applied[i].Version, m.Version)
		}
	}

	again, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Up applied %+v, want none", again)
	}

	for _, m := range Models() {
		if !db.Migrator().HasTable(m) {
			t.Fatalf("table missing for %T", m)
		}
	}
}

// TestAllMigrationsUpgradeFromVersionOne simulates a store whose ledger says
// only migration 1 ran, then applies 2 and 3 and checks the additive schema
// transitions they own.
func TestAllMigrationsUpgradeFromVersionOne(t *testing.T) {
	t.Parallel()
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
	name := "baleia_upgrade_" + strings.ToLower(rand.Text()[:12])
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
	db, err := Open(ctx, u.String(), Postgres, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })

	// The version-1 schema, before setup locks and the sync heartbeat.
	if err := AutoMigrate(db, Models()...); err != nil {
		t.Fatalf("version-1 schema: %v", err)
	}
	if err := db.Migrator().DropTable(&model.SetupLock{}); err != nil {
		t.Fatalf("drop setup_locks: %v", err)
	}
	if err := db.Migrator().DropColumn(&model.MetadataSyncRun{}, "heartbeat_at"); err != nil {
		t.Fatalf("drop heartbeat_at: %v", err)
	}
	if err := db.Migrator().DropColumn(&model.CatalogConnection{}, "access_mode"); err != nil {
		t.Fatalf("drop access_mode: %v", err)
	}
	// Replace the constraint with the older, narrower one so migration 6 has to
	// expand it instead of finding the current model's list already in place.
	if err := db.Exec("ALTER TABLE catalog_connections DROP CONSTRAINT catalog_connections_type_check").Error; err != nil {
		t.Fatalf("drop current type check: %v", err)
	}
	oldCheck := "type = ANY (ARRAY['polaris'::text, 'nessie'::text, 'iceberg'::text, 'lakekeeper'::text, 'unity'::text, 'hive'::text, 'glue'::text, 'postgresql'::text, 'mysql'::text, 'mariadb'::text, 'sqlserver'::text, 'oracle'::text, 'sqlite'::text, 'clickhouse'::text, 'flightsql'::text, 'trino'::text, 'doris'::text, 'starrocks'::text])"
	if err := db.Exec("ALTER TABLE catalog_connections ADD CONSTRAINT catalog_connections_type_check CHECK (" + oldCheck + ")").Error; err != nil {
		t.Fatalf("add old type check: %v", err)
	}
	// The old constraint must reject a kind that only migration 6 adds.
	if err := db.Exec("INSERT INTO catalog_connections (id, name, type, url, config) VALUES (gen_random_uuid(), 'probe-before', 'bigquery', '', '{}')").Error; err == nil {
		t.Fatal("the old constraint accepted a kind added by migration 6")
	}

	reg, err := AllMigrations()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(db, reg, slog.New(slog.DiscardHandler))
	if err := runner.ensureTracking(ctx); err != nil {
		t.Fatalf("tracking tables: %v", err)
	}
	if err := db.Create(&SchemaMigration{Version: 1, Name: "create store schema", AppliedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("record version 1: %v", err)
	}

	applied, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 5 || applied[0].Version != 2 || applied[1].Version != 3 || applied[2].Version != 4 || applied[3].Version != 5 || applied[4].Version != 6 {
		t.Fatalf("applied = %+v, want versions 2, 3, 4, 5 and 6", applied)
	}
	if !db.Migrator().HasTable(&model.SetupLock{}) {
		t.Fatal("setup_locks was not created by migration 2")
	}
	if !db.Migrator().HasColumn(&model.MetadataSyncRun{}, "heartbeat_at") {
		t.Fatal("heartbeat_at was not created by migration 3")
	}
	if !db.Migrator().HasColumn(&model.CatalogConnection{}, "access_mode") {
		t.Fatal("access_mode was not created by migration 5")
	}
	var constraint string
	if err := db.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'catalog_connections_type_check'").Scan(&constraint).Error; err != nil {
		t.Fatalf("read connection type check: %v", err)
	}
	if !strings.Contains(constraint, "'bigquery'") || !strings.Contains(constraint, "'system'") {
		t.Fatalf("connection type check was not expanded by migration 6: %s", constraint)
	}
	// The expanded constraint accepts the kind the old one rejected.
	if err := db.Exec("INSERT INTO catalog_connections (id, name, type, url, config) VALUES (gen_random_uuid(), 'probe-after', 'bigquery', '', '{}')").Error; err != nil {
		t.Fatalf("the expanded constraint rejected a declared kind: %v", err)
	}
	var versions []int64
	if err := db.Model(&SchemaMigration{}).Order("version").Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != 6 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 || versions[3] != 4 || versions[4] != 5 || versions[5] != 6 {
		t.Fatalf("ledger versions = %v, want 1, 2, 3, 4, 5, 6", versions)
	}
}
