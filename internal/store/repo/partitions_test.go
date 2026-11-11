package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestEnsureAuditPartitions: the schedule extends the partitions of the audit
// trail and the query history ahead of the current month, idempotently, and
// never drops one.
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
	for range 2 {
		if err := r.EnsureAuditPartitions(ctx, 6); err != nil {
			t.Fatalf("EnsureAuditPartitions: %v", err)
		}
	}
	last := partitioning.Monthly.Start(time.Now()).AddDate(0, 6, 0)
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
