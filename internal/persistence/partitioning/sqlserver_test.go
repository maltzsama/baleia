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
		_ = db.Exec(`DROP PARTITION SCHEME [ps_partitioning_test_events_monthly]`).Error
		_ = db.Exec(`DROP PARTITION FUNCTION [pf_partitioning_test_events_monthly]`).Error
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

// TestSQLServerConvertsPlainTable moves a populated plain table onto the
// partition scheme in place and then reclaims a month, which only works when the
// secondary indexes were rebuilt aligned with the table.
func TestSQLServerConvertsPlainTable(t *testing.T) {
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
	t.Cleanup(func() {
		_ = db.Exec(`DROP TABLE IF EXISTS [partitioning_conv_events]`).Error
		_ = db.Exec(`DROP PARTITION SCHEME [ps_partitioning_conv_events_monthly]`).Error
		_ = db.Exec(`DROP PARTITION FUNCTION [pf_partitioning_conv_events_monthly]`).Error
	})
	_ = db.Exec(`DROP TABLE IF EXISTS [partitioning_conv_events]`).Error
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
	if err := pm.Provision(ctx, &convEvent{}, spec, from, until); err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	fresh := convEvent{OccurredAt: mar, Name: "new"}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.ID != 3 {
		t.Fatalf("identity resumed at %d, want 3", fresh.ID)
	}
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := pm.DropPartition(ctx, spec.Table, jan, feb); err != nil {
		t.Fatalf("DropPartition on the converted table: %v", err)
	}
	var n int64
	if err := db.Model(&convEvent{}).Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("rows after drop = %d, %v; want 2", n, err)
	}
}

// TestSQLServerSparseDailyPartitions: pruning can leave daily boundaries as far
// apart as a month's, and the width must still be a day. It is the case a width
// guessed from the boundaries gets wrong: boundaries 28 days apart read as
// monthly, so the partition ending Dec 30 would start Nov 30, not Dec 29, and a
// drop of Dec 29 would be judged against the wrong day.
//
// It is not parallel: it shares the server and the fixed table name with the
// other tests of the engine, and serial tests finish before parallel ones resume.
//
//nolint:paralleltest // see above
func TestSQLServerSparseDailyPartitions(t *testing.T) {
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
		_ = db.Exec(`DROP PARTITION SCHEME [ps_partitioning_test_events_daily]`).Error
		_ = db.Exec(`DROP PARTITION FUNCTION [pf_partitioning_test_events_daily]`).Error
	})
	ctx := context.Background()
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatal(err)
	}
	spec := partitioning.Spec{Table: "partitioning_test_events", Column: "occurred_at", Strategy: partitioning.Range, Interval: partitioning.Daily}
	day := func(m time.Month, d int) time.Time {
		return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 0)
	}
	jan26 := time.Date(2027, 1, 26, 0, 0, 0, 0, time.UTC)

	// Dec 1 through Jan 26: 57 days.
	if err := pm.Provision(ctx, &testEvent{}, spec, day(time.December, 1), jan26.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for i, at := range []time.Time{day(time.December, 1).Add(time.Hour), day(time.December, 29).Add(time.Hour), jan26.Add(time.Hour)} {
		if err := db.Create(&testEvent{ID: int64(i + 1), OccurredAt: at, Name: "x"}).Error; err != nil {
			t.Fatalf("insert %v: %v", at, err)
		}
	}
	// Prune everything between the three days that stay, leaving boundaries
	// exactly 28 days apart.
	if err := pm.DropPartition(ctx, spec.Table, day(time.December, 2), day(time.December, 29)); err != nil {
		t.Fatalf("DropPartition (first span): %v", err)
	}
	if err := pm.DropPartition(ctx, spec.Table, day(time.December, 30), jan26); err != nil {
		t.Fatalf("DropPartition (second span): %v", err)
	}

	parts, err := pm.ListPartitions(ctx, spec.Table)
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	wantFrom := []time.Time{day(time.December, 1), day(time.December, 29), jan26}
	if len(parts) != len(wantFrom) {
		t.Fatalf("partitions = %+v, want the %d days that stay", parts, len(wantFrom))
	}
	for i, from := range wantFrom {
		if !parts[i].From.Equal(from) || parts[i].Until.Sub(parts[i].From) != 24*time.Hour {
			t.Errorf("partition %d = [%v, %v), want the day starting %v", i, parts[i].From, parts[i].Until, from)
		}
	}

	// Dropping Dec 29 removes that day's row and nothing else.
	if err := pm.DropPartition(ctx, spec.Table, day(time.December, 29), day(time.December, 30)); err != nil {
		t.Fatalf("DropPartition (Dec 29): %v", err)
	}
	var ids []int64
	if err := db.Table(spec.Table).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("surviving row ids = %v, want [1 3]", ids)
	}

	// A table is partitioned with one width: asking for the other is refused.
	monthly := spec
	monthly.Interval = partitioning.Monthly
	if err := pm.Provision(ctx, &testEvent{}, monthly, day(time.December, 1), day(time.December, 1).AddDate(0, 2, 0)); err == nil {
		t.Fatal("Provision with another width should be refused")
	}
}
