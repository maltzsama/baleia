package partitioning_test

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type sqliteEvent struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement:false"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
	Name       string    `gorm:"column:name"`
}

func (sqliteEvent) TableName() string { return "partitioning_test_events" }

func openSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// TestSQLiteEmulatedPartitions exercises the emulated adapter end to end:
// provision (create-or-extend), list, insert, and a bounded-DELETE drop.
// SQLite has no native partitioning, so the adapter must report Emulated and
// reclaim a month with a DELETE rather than a metadata drop.
func TestSQLiteEmulatedPartitions(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()

	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c := pm.Capabilities(); c.NativeRange || !c.Emulated || !c.Implemented {
		t.Fatalf("capabilities = %+v, want emulated (not native)", c)
	}

	spec := partitioning.Spec{
		Table:    "partitioning_test_events",
		Column:   "occurred_at",
		Strategy: partitioning.Range,
		Interval: partitioning.Monthly,
	}
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	apr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &sqliteEvent{}, spec, jan, apr); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// Idempotent: a second call leaves the table and registry alone.
	if err := pm.Provision(ctx, &sqliteEvent{}, spec, jan, apr); err != nil {
		t.Fatalf("Provision (second): %v", err)
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("ListPartitions returned %d partitions, want 3: %+v", len(parts), parts)
	}
	wantNames := []string{"partitioning_test_events_202601", "partitioning_test_events_202602", "partitioning_test_events_202603"}
	for i, want := range wantNames {
		if parts[i].Name != want {
			t.Errorf("partition %d = %q, want %q", i, parts[i].Name, want)
		}
	}

	for i, m := range []time.Time{
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
	} {
		if err := db.Create(&sqliteEvent{ID: int64(i + 1), OccurredAt: m, Name: "x"}).Error; err != nil {
			t.Fatalf("insert month %v: %v", m, err)
		}
	}

	// Dropping February removes exactly its rows and its registry entry.
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, feb, mar); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	var n int64
	if err := db.Model(&sqliteEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows after drop = %d, want 2", n)
	}
	parts, err = pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions after drop: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("after drop: %d partitions, want 2", len(parts))
	}
}

// TestSQLiteUnprovisionedTable reports no partitions for a table that was
// never provisioned, and drops it without error.
func TestSQLiteUnprovisionedTable(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	parts, err := pm.ListPartitions(ctx, "never_provisioned")
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("ListPartitions = %+v, want empty", parts)
	}
	if err := pm.DropPartition(ctx, "never_provisioned", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("DropPartition on unprovisioned table: %v", err)
	}
}
