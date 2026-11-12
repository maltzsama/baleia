package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestEnsureAuditPartitions: the schedule extends the daily partitions of the
// audit trail and the query history ahead of today, idempotently, and never
// drops one.
func TestEnsureAuditPartitions(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	if !partitioning.CapabilitiesFor(gdb).NativeRange {
		t.Skip("native range partitioning required")
	}
	r := New(gdb)
	ctx := context.Background()
	pm, err := partitioning.New(gdb)
	if err != nil {
		t.Fatal(err)
	}
	// Past the 14 days the migration provisions, so the call really extends.
	const ahead = 30
	for range 2 {
		if err := r.EnsureAuditPartitions(ctx, ahead); err != nil {
			t.Fatalf("EnsureAuditPartitions: %v", err)
		}
	}
	last := partitioning.Daily.Start(time.Now()).AddDate(0, 0, ahead)
	for _, tbl := range model.PartitionedTables() {
		parts, err := pm.ListPartitions(ctx, tbl.Table)
		if err != nil {
			t.Fatal(err)
		}
		if len(parts) == 0 || !parts[len(parts)-1].From.Equal(last) {
			t.Errorf("%s: partitions = %+v, want the last to start %v", tbl.Table, parts, last)
		}
	}
}

// TestFreshDatabaseAcceptsYesterday: a freshly migrated database has a partition
// for the day before today. The audit-partitions schedule only ever looks
// forward, so the first day of a database's life is the one day nothing would
// create it for, and an event stamped just before midnight (clock skew between
// replicas, a write that straddles the boundary, or a test that backdates by
// minutes just after 00:00 UTC) must not fail for want of a partition.
func TestFreshDatabaseAcceptsYesterday(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	justBeforeMidnight := partitioning.Daily.Start(time.Now()).Add(-time.Hour)

	if err := f.gdb.Create(&model.AuditEvent{
		OccurredAt: justBeforeMidnight, ActorKind: "user", Action: "catalog.update",
		ResourceType: "catalog", Outcome: "allowed",
	}).Error; err != nil {
		t.Fatalf("an audit event from yesterday: %v", err)
	}
	if err := f.gdb.Create(&model.QueryExecution{
		ID: model.UUID(NewUUID()), WorkspaceID: f.ws, UserID: f.owner,
		Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: justBeforeMidnight,
	}).Error; err != nil {
		t.Fatalf("a query execution from yesterday: %v", err)
	}
}
