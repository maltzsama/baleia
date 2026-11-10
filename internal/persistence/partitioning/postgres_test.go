package partitioning_test

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/testdb"
)

type testEvent struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement:false"`
	OccurredAt time.Time `gorm:"column:occurred_at;primaryKey;not null"`
	Name       string    `gorm:"column:name"`
}

func (testEvent) TableName() string { return "partitioning_test_events" }

// TestPostgresPartitions exercises the adapter end to end against a real
// PostgreSQL: parent creation, monthly provisioning, routing, listing and
// dropping. It skips without BALEIA_TEST_DATABASE_URL, like the rest of the
// DB-backed suite.
func TestPostgresPartitions(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := context.Background()

	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := partitioning.Spec{
		Table:    "partitioning_test_events",
		Column:   "occurred_at",
		Strategy: partitioning.Range,
		Interval: partitioning.Monthly,
	}

	if c := pm.Capabilities(); !c.NativeRange || !c.Implemented {
		t.Fatalf("capabilities = %+v, want native range implemented", c)
	}
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	apr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &testEvent{}, spec, jan, apr); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS "partitioning_test_events" CASCADE`).Error })
	// Idempotent: a second call leaves the existing table and partitions alone.
	if err := pm.Provision(ctx, &testEvent{}, spec, jan, apr); err != nil {
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

	// Rows route to the partition that owns their month.
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

	// Dropping a partition removes exactly its rows.
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, feb, mar); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	parts, err = pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions after drop: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("after drop: %d partitions, want 2", len(parts))
	}
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows after drop = %d, want 2", n)
	}
}
