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

// plainConvEvent is the table as a plain deployment has it: id alone is the key.
type plainConvEvent struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
	Name       string    `gorm:"column:name;index:idx_conv_events_name"`
}

func (plainConvEvent) TableName() string { return "partitioning_conv_events" }

// convEvent is the same table once partitioned: the month joins the key.
type convEvent struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	OccurredAt time.Time `gorm:"column:occurred_at;primaryKey;not null"`
	Name       string    `gorm:"column:name;index:idx_conv_events_name"`
}

func (convEvent) TableName() string { return "partitioning_conv_events" }

// TestPostgresConvertsPlainTable covers the swap: a populated plain table is set
// aside, the partitioned table takes its name and key, the rows are copied with
// their ids, and the sequence resumes after the largest id.
func TestPostgresConvertsPlainTable(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
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
	legacy := partitioning.LegacyTable(spec.Table)
	if !db.Migrator().HasTable(legacy) {
		t.Fatalf("%s should hold the old rows", legacy)
	}
	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil || len(parts) != 4 {
		t.Fatalf("partitions = %+v, %v; want 4", parts, err)
	}
	// Idempotent once converted: the partitioned table is left alone.
	if err := pm.Provision(ctx, &convEvent{}, spec, from, until); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	var old []plainConvEvent
	if err := db.Table(legacy).Order("id").Find(&old).Error; err != nil || len(old) != 2 {
		t.Fatalf("legacy rows = %+v, %v", old, err)
	}
	for _, row := range old {
		moved := convEvent(row)
		if err := db.Create(&moved).Error; err != nil {
			t.Fatalf("copy id %d: %v", row.ID, err)
		}
	}
	syncer, ok := pm.(partitioning.SequenceSyncer)
	if !ok {
		t.Fatal("the PostgreSQL adapter must sync sequences")
	}
	if err := syncer.SyncSequence(ctx, spec.Table, "id", old[len(old)-1].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(legacy); err != nil {
		t.Fatal(err)
	}

	fresh := convEvent{OccurredAt: mar, Name: "new"}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.ID != old[len(old)-1].ID+1 {
		t.Fatalf("next id = %d, want %d", fresh.ID, old[len(old)-1].ID+1)
	}
}

// TestPostgresRefusesInterruptedConversion: a plain table next to a legacy one
// is an interrupted conversion that must not be repeated blindly.
func TestPostgresRefusesInterruptedConversion(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&plainConvEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE partitioning_conv_events_legacy (id bigint)`).Error; err != nil {
		t.Fatal(err)
	}
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatal(err)
	}
	spec := partitioning.Spec{Table: "partitioning_conv_events", Column: "occurred_at", Strategy: partitioning.Range, Interval: partitioning.Monthly}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &convEvent{}, spec, from, from.AddDate(0, 1, 0)); err == nil {
		t.Fatal("Provision should refuse a plain table beside an existing legacy table")
	}
}

// TestPostgresDailyPartitions covers the daily width end to end: day-wide
// ranges, names with a yyyymmdd suffix, routing, listing, and a drop that
// removes exactly the days inside the range.
func TestPostgresDailyPartitions(t *testing.T) {
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
		Interval: partitioning.Daily,
	}
	from := time.Date(2026, 11, 29, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC)
	if err := pm.Provision(ctx, &testEvent{}, spec, from, until); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS "partitioning_test_events" CASCADE`).Error })
	if err := pm.Provision(ctx, &testEvent{}, spec, from, until); err != nil {
		t.Fatalf("Provision (second): %v", err)
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	want := []string{
		"partitioning_test_events_20261129", "partitioning_test_events_20261130",
		"partitioning_test_events_20261201", "partitioning_test_events_20261202",
	}
	if len(parts) != len(want) {
		t.Fatalf("ListPartitions returned %d partitions, want %d: %+v", len(parts), len(want), parts)
	}
	for i, name := range want {
		if parts[i].Name != name {
			t.Errorf("partition %d = %q, want %q", i, parts[i].Name, name)
		}
		if got := parts[i].Until.Sub(parts[i].From); got != 24*time.Hour {
			t.Errorf("partition %d spans %v, want 24h", i, got)
		}
	}

	// One row per day; the last instant of a day stays in that day.
	for i, at := range []time.Time{
		time.Date(2026, 11, 29, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC),
	} {
		if err := db.Create(&testEvent{ID: int64(i + 1), OccurredAt: at, Name: "x"}).Error; err != nil {
			t.Fatalf("insert %v: %v", at, err)
		}
	}
	// A day past the provisioned horizon has no partition: the insert fails
	// instead of landing somewhere else.
	if err := db.Create(&testEvent{ID: 99, OccurredAt: until, Name: "x"}).Error; err == nil {
		t.Fatal("insert beyond the horizon should fail")
	}

	if err := pm.DropPartition(ctx, spec.Table,
		time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("DropPartition: %v", err)
	}
	parts, err = pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions after drop: %v", err)
	}
	if len(parts) != 2 || parts[0].Name != want[0] || parts[1].Name != want[3] {
		t.Fatalf("after drop: %+v, want the first and last day", parts)
	}
	var n int64
	if err := db.Model(&testEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count after drop: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows after drop = %d, want 2", n)
	}
}
