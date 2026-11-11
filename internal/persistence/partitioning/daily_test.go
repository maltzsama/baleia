package partitioning_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/testdb"
	oracle "github.com/godoes/gorm-oracle"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

// exerciseDaily drives the daily width through an engine's adapter end to end:
// provision, extend, list (names and widths), route rows, drop. It is
// engine-neutral: the engines differ in the DDL behind it, not in this
// contract. Engines with a catch-all partition accept a row past the horizon, so
// that is not asserted here (the PostgreSQL test does).
func exerciseDaily(t *testing.T, db *gorm.DB, model any) {
	t.Helper()
	ctx := context.Background()
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := partitioning.Spec{Table: "partitioning_test_events", Column: "occurred_at", Strategy: partitioning.Range, Interval: partitioning.Daily}
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }

	if err := pm.Provision(ctx, model, spec, day(time.November, 29), day(time.December, 3)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// Extend by a day, as the schedule does, and again to prove idempotence.
	for range 2 {
		if err := pm.Provision(ctx, model, spec, day(time.November, 29), day(time.December, 4)); err != nil {
			t.Fatalf("Provision (extend): %v", err)
		}
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	want := []string{"20261129", "20261130", "20261201", "20261202", "20261203"}
	if len(parts) != len(want) {
		t.Fatalf("ListPartitions returned %d partitions, want %d: %+v", len(parts), len(want), parts)
	}
	for i, suffix := range want {
		if parts[i].Name != spec.Table+"_"+suffix {
			t.Errorf("partition %d = %q, want suffix %s", i, parts[i].Name, suffix)
		}
		if got := parts[i].Until.Sub(parts[i].From); got != 24*time.Hour {
			t.Errorf("partition %d spans %v, want 24h", i, got)
		}
	}

	for i, at := range []time.Time{
		time.Date(2026, 11, 29, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 3, 6, 0, 0, 0, time.UTC),
	} {
		if err := db.Table(spec.Table).Create(map[string]any{"id": int64(i + 1), "occurred_at": at, "name": "x"}).Error; err != nil {
			t.Fatalf("insert %v: %v", at, err)
		}
	}
	count := func() int64 {
		t.Helper()
		var n int64
		if err := db.Table(spec.Table).Count(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := count(); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}

	// Dropping Nov 30 and Dec 1 removes exactly their rows and partitions.
	if err := pm.DropPartition(ctx, spec.Table, day(time.November, 30), day(time.December, 2)); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	if n := count(); n != 3 {
		t.Fatalf("rows after drop = %d, want 3", n)
	}
	// The count alone cannot tell the right days were dropped: the survivors are
	// the rows of Nov 29, Dec 2 and Dec 3.
	var ids []int64
	if err := db.Table(spec.Table).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatalf("surviving ids: %v", err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 4 || ids[2] != 5 {
		t.Fatalf("surviving row ids = %v, want [1 4 5] (Nov 29, Dec 2, Dec 3)", ids)
	}
	parts, err = pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions after drop: %v", err)
	}
	if len(parts) != 3 || parts[0].Name != spec.Table+"_20261129" || parts[1].Name != spec.Table+"_20261202" || parts[2].Name != spec.Table+"_20261203" {
		t.Fatalf("after drop: %+v, want days 29, Dec 2 and Dec 3", parts)
	}
}

func TestMySQLDailyPartitions(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	if db.Name() != "mysql" {
		t.Skip("requires a MySQL/MariaDB test database")
	}
	exerciseDaily(t, db, &testEvent{})
}

// TestSQLServerDailyPartitions is not parallel: it shares the server and the fixed table name with the
// other tests of the engine, and serial tests finish before parallel ones resume.
//
//nolint:paralleltest // see above
func TestSQLServerDailyPartitions(t *testing.T) {
	dsn := os.Getenv("BALEIA_TEST_SQLSERVER_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_SQLSERVER_URL not set")
	}
	db, err := gorm.Open(sqlserver.Open(dsn+"?encrypt=disable&database=master"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlserver: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DROP TABLE IF EXISTS [partitioning_test_events]`).Error
		_ = db.Exec(`DROP PARTITION SCHEME [ps_partitioning_test_events]`).Error
		_ = db.Exec(`DROP PARTITION FUNCTION [pf_partitioning_test_events]`).Error
	})
	exerciseDaily(t, db, &testEvent{})
}

// TestOracleDailyPartitions is not parallel: it shares the server and the fixed table name with the
// other tests of the engine, and serial tests finish before parallel ones resume.
//
//nolint:paralleltest // see above
func TestOracleDailyPartitions(t *testing.T) {
	dsn := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	db, err := gorm.Open(oracle.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE partitioning_test_events`).Error })
	exerciseDaily(t, db, &oracleEvent{})
}
