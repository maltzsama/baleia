package partitioning_test

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestMySQLPartitions exercises the MySQL/MariaDB native range adapter end to
// end: create-or-extend (via the MAXVALUE catch-all + REORGANIZE), routing,
// listing and a metadata drop. It runs only when the test database is
// MySQL/MariaDB; otherwise it skips.
func TestMySQLPartitions(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	if db.Name() != "mysql" {
		t.Skip("partitioning MySQL test requires a MySQL/MariaDB test database")
	}
	ctx := context.Background()

	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c := pm.Capabilities(); !c.NativeRange || c.ForeignKeysOnPartitioned || !c.Implemented {
		t.Fatalf("capabilities = %+v, want native range, no FK on partitioned", c)
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
	// Extend by a month: splits April off the catch-all, leaving the existing
	// partitions alone.
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &testEvent{}, spec, jan, may); err != nil {
		t.Fatalf("Provision (extend): %v", err)
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("ListPartitions returned %d partitions, want 4: %+v", len(parts), parts)
	}

	for i, m := range []time.Time{
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
	} {
		if err := db.Create(&testEvent{ID: int64(i + 1), OccurredAt: m, Name: "x"}).Error; err != nil {
			t.Fatalf("insert month %v: %v", m, err)
		}
	}
	var n int64
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 4 {
		t.Fatalf("rows = %d, want 4", n)
	}

	// Dropping February removes exactly its rows.
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, feb, mar); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 3 {
		t.Fatalf("rows after drop = %d, want 3", n)
	}
	parts, err = pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions after drop: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("after drop: %d partitions, want 3", len(parts))
	}
}

// TestMySQLConvertsPlainTable partitions a populated plain table in place: the
// primary key gains the partition column in the same rebuild, the rows and the
// auto-increment survive.
func TestMySQLConvertsPlainTable(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	if db.Name() != "mysql" {
		t.Skip("partitioning MySQL test requires a MySQL/MariaDB test database")
	}
	ctx := context.Background()
	if err := db.AutoMigrate(&plainConvEvent{}); err != nil {
		t.Fatal(err)
	}
	jan := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{jan, mar} {
		if err := db.Create(&plainConvEvent{OccurredAt: at, Name: "x"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatal(err)
	}
	spec := partitioning.Spec{Table: "partitioning_conv_events", Column: "occurred_at", Strategy: partitioning.Range, Interval: partitioning.Monthly}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &convEvent{}, spec, from, until); err != nil {
		t.Fatalf("Provision over a plain table: %v", err)
	}
	// Idempotent once converted.
	if err := pm.Provision(ctx, &convEvent{}, spec, from, until); err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil || len(parts) != 4 {
		t.Fatalf("partitions = %+v, %v; want 4", parts, err)
	}
	var n int64
	if err := db.Model(&convEvent{}).Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("rows = %d, %v; want 2", n, err)
	}
	fresh := convEvent{OccurredAt: mar, Name: "new"}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.ID != 3 {
		t.Fatalf("auto-increment resumed at %d, want 3", fresh.ID)
	}
	// The month is part of the key, so the same id may exist in another month.
	if err := db.Create(&convEvent{ID: 1, OccurredAt: time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC), Name: "dup"}).Error; err != nil {
		t.Fatalf("composite key should allow id 1 in another month: %v", err)
	}
}
