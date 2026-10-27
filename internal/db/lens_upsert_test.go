package db

import (
	"strings"
	"testing"
)

// TestUpsertTableHealthSnapshotUpdatesOnConflict guards #137: the query is named
// upsert, and table_health_snapshots is UNIQUE per table, so a second capture
// must update the row instead of failing with a duplicate key.
func TestUpsertTableHealthSnapshotUpdatesOnConflict(t *testing.T) {
	t.Parallel()
	q := upsertTableHealthSnapshot
	for _, want := range []string{
		"ON CONFLICT (catalog_id, namespace, table_name) DO UPDATE",
		"captured_at",
		"health_score              = EXCLUDED.health_score",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("upsert query does not contain %q:\n%s", want, q)
		}
	}
}
