package partitioning_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

// TestSQLServerPartitions exercises the SQL Server adapter against a live
// server named by BALEIA_TEST_SQLSERVER_URL; it skips otherwise. The adapter
// partitions via a partition function + scheme and reclaims a month with
// truncate + merge, which this test drives end to end.
func TestSQLServerPartitions(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("BALEIA_TEST_SQLSERVER_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_SQLSERVER_URL not set")
	}
	db, err := gorm.Open(sqlserver.Open(dsn+"?encrypt=disable&database=master"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlserver: %v", err)
	}
	ctx := context.Background()

	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c := pm.Capabilities(); !c.NativeRange || !c.ForeignKeysOnPartitioned || !c.Implemented {
		t.Fatalf("capabilities = %+v", c)
	}

	spec := partitioning.Spec{
		Table:    "partitioning_test_events",
		Column:   "occurred_at",
		Strategy: partitioning.Range,
		Interval: partitioning.Monthly,
	}
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	apr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &testEvent{}, spec, jan, apr); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DROP TABLE IF EXISTS [partitioning_test_events]`).Error
		_ = db.Exec(`DROP PARTITION SCHEME [ps_partitioning_test_events]`).Error
		_ = db.Exec(`DROP PARTITION FUNCTION [pf_partitioning_test_events]`).Error
	})

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("ListPartitions returned %d, want 3: %+v", len(parts), parts)
	}

	for i, m := range []time.Time{
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
	} {
		if err := db.Create(&testEvent{ID: int64(i + 1), OccurredAt: m, Name: "x"}).Error; err != nil {
			t.Fatalf("insert month %v: %v", m, err)
		}
	}
	var n int64
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}

	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, feb, mar); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows after drop = %d, want 2", n)
	}
}
