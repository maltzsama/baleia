package store

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
)

func TestAllMigrationsUpCreatesSchema(t *testing.T) {
	t.Parallel()
	adminURL := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, adminURL, Postgres, Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(admin) })

	name := "baleia_migrate_" + strings.ToLower(rand.Text()[:12])
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
	ctx := context.Background()
	admin, err := Open(ctx, adminURL, Postgres, Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(admin) })

	name := "baleia_upgrade_" + strings.ToLower(rand.Text()[:12])
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
	if err := db.Migrator().DropTable(&model.MonitoringEvent{}); err != nil {
		t.Fatalf("drop monitoring_events: %v", err)
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
	all := reg.All()
	if len(applied) != len(all)-1 {
		t.Fatalf("applied = %+v, want the %d pending migrations", applied, len(all)-1)
	}
	for i, m := range all[1:] {
		if applied[i].Version != m.Version {
			t.Fatalf("applied[%d] = %d, want %d", i, applied[i].Version, m.Version)
		}
	}
	if !db.Migrator().HasTable(&model.SetupLock{}) {
		t.Fatal("setup_locks was not created by migration 2")
	}
	if !db.Migrator().HasColumn(&model.MetadataSyncRun{}, "heartbeat_at") {
		t.Fatal("heartbeat_at was not created by migration 3")
	}
	if !db.Migrator().HasTable(&model.MonitoringEvent{}) {
		t.Fatal("monitoring_events was not created by migration 7")
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
	var versions []int64
	if err := db.Model(&SchemaMigration{}).Order("version").Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != len(all) {
		t.Fatalf("ledger versions = %v, want %d versions", versions, len(all))
	}
	for i, m := range all {
		if versions[i] != m.Version {
			t.Fatalf("ledger versions = %v, want %d at index %d", versions, m.Version, i)
		}
	}
}
