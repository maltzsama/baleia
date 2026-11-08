package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type runnerTestRow struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:64;not null"`
}

func (runnerTestRow) TableName() string { return "runner_test_rows" }

// openTestStore opens the GORM store over a throwaway database migrated by the
// legacy goose path, which is the upgrade case the runner has to adopt.
func openTestStore(t *testing.T) *gorm.DB {
	t.Helper()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open gorm store: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := NewRegistry(Migration{
		Version: 1,
		Name:    "create runner_test_rows",
		Up:      func(db *gorm.DB) error { return db.AutoMigrate(&runnerTestRow{}) },
		Down:    func(db *gorm.DB) error { return db.Migrator().DropTable(&runnerTestRow{}) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestNewRegistryValidates(t *testing.T) {
	t.Parallel()
	up := func(*gorm.DB) error { return nil }
	if _, err := NewRegistry(
		Migration{Version: 1, Name: "a", Up: up},
		Migration{Version: 1, Name: "b", Up: up},
	); err == nil {
		t.Fatal("duplicate versions accepted")
	}
	if _, err := NewRegistry(Migration{Version: 0, Name: "zero", Up: up}); err == nil {
		t.Fatal("baseline version accepted as a migration")
	}
	if _, err := NewRegistry(Migration{Version: 1, Name: "", Up: up}); err == nil {
		t.Fatal("nameless migration accepted")
	}
	if _, err := NewRegistry(Migration{Version: 1, Name: "no up"}); err == nil {
		t.Fatal("migration without Up accepted")
	}
	if _, err := NewRegistry(Migration{Version: 1, Name: strings.Repeat("x", 256), Up: up}); err == nil {
		t.Fatal("over-long migration name accepted")
	}
}

func TestRunnerAdoptsGooseBaseline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}

	applied, err := NewRunner(db, reg, slog.New(slog.DiscardHandler)).Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none on a goose baseline", applied)
	}

	var rows []SchemaMigration
	if err := db.Order("version").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Version != BaselineVersion {
		t.Fatalf("ledger = %+v, want one baseline row at version %d", rows, BaselineVersion)
	}
}

func TestRunnerReportsFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)
	reg, err := NewRegistry(Migration{
		Version: 1,
		Name:    "boom",
		Up:      func(*gorm.DB) error { return errors.New("boom") },
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(db, reg, slog.New(slog.DiscardHandler))
	_, err = runner.Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "migration 1 (boom)") {
		t.Fatalf("Up error = %v, want the failing migration identified", err)
	}
	var rows []SchemaMigration
	if err := db.Where("version = ?", 1).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Dirty {
		t.Fatalf("ledger = %+v, want one dirty row for migration 1", rows)
	}
	if _, err := runner.Up(ctx); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("second Up error = %v, want a retry refusal until reconciliation", err)
	}
}

func TestRunnerStatusIsReadOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)
	if db.Migrator().HasTable(&SchemaMigration{}) {
		t.Skip("test database already has a schema_migrations table")
	}

	statuses, err := NewRunner(db, testRegistry(t), slog.New(slog.DiscardHandler)).Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Applied {
		t.Fatalf("status = %+v, want migration 1 pending", statuses)
	}
	if db.Migrator().HasTable(&SchemaMigration{}) {
		t.Fatal("Status created the ledger")
	}
}

func TestRunnerRejectsConcurrentRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)

	first := NewRunner(db, testRegistry(t), slog.New(slog.DiscardHandler))
	if err := first.ensureTracking(ctx); err != nil {
		t.Fatal(err)
	}
	release, err := first.acquireLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	second := NewRunner(db, testRegistry(t), slog.New(slog.DiscardHandler))
	if _, err := second.Up(ctx); err == nil || !strings.Contains(err.Error(), "another store migration is running") {
		t.Fatalf("Up error = %v, want the lock refusal", err)
	}

	release()
	if _, err := second.Up(ctx); err != nil {
		t.Fatalf("Up after release: %v", err)
	}
}

func TestRunnerRefusesUnsafeRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)
	reg, err := NewRegistry(Migration{
		Version: 1,
		Name:    "irreversible",
		Up:      func(db *gorm.DB) error { return db.AutoMigrate(&runnerTestRow{}) },
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(db, reg, slog.New(slog.DiscardHandler))
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := runner.Down(ctx); err == nil || !strings.Contains(err.Error(), "no safe rollback") {
		t.Fatalf("Down error = %v, want the unsafe rollback refusal", err)
	}
}

func TestRunnerAppliesTracksAndRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t)
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

	again, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Up applied %+v, want none", again)
	}

	statuses, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 2 || statuses[0].Version != BaselineVersion || !statuses[1].Applied {
		t.Fatalf("status = %+v, want baseline and applied migration 1", statuses)
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

	statuses, err = runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status after Down: %v", err)
	}
	if len(statuses) != 2 || statuses[1].Applied {
		t.Fatalf("status = %+v, want migration 1 pending again", statuses)
	}
}
