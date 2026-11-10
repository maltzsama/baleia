package partitioning_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	oracle "github.com/godoes/gorm-oracle"
	"gorm.io/gorm"
)

// oracleEvent is the Oracle test table. The partition column is TIMESTAMP
// (not TIMESTAMP WITH TIME ZONE), because Oracle forbids the latter in a
// primary key, and the partition key must be part of the key.
type oracleEvent struct {
	ID         int64     `gorm:"column:id;primaryKey"`
	OccurredAt time.Time `gorm:"column:occurred_at;primaryKey;not null;type:timestamp"`
	Name       string    `gorm:"column:name"`
}

func (oracleEvent) TableName() string { return "partitioning_test_events" }

// TestOraclePartitions exercises the Oracle adapter against a live server named
// by BALEIA_TEST_ORACLE_URL (a go-ora URL); it skips otherwise. It uses the
// pure-Go go-ora dialector, so it needs no Oracle Instant Client.
func TestOraclePartitions(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	db, err := gorm.Open(oracle.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open oracle: %v", err)
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
	if err := pm.Provision(ctx, &oracleEvent{}, spec, jan, apr); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE partitioning_test_events`).Error })
	// Extend by a month: splits April off the MAXVALUE catch-all.
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &oracleEvent{}, spec, jan, may); err != nil {
		t.Fatalf("Provision (extend): %v", err)
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("ListPartitions returned %d, want 4: %+v", len(parts), parts)
	}

	for i, m := range []time.Time{
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
	} {
		if err := db.Create(&oracleEvent{ID: int64(i + 1), OccurredAt: m, Name: "x"}).Error; err != nil {
			t.Fatalf("insert month %v: %v", m, err)
		}
	}
	var n int64
	if err := db.Model(&oracleEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 4 {
		t.Fatalf("rows = %d, want 4", n)
	}

	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, feb, mar); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	if err := db.Model(&oracleEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 3 {
		t.Fatalf("rows after drop = %d, want 3", n)
	}
}
