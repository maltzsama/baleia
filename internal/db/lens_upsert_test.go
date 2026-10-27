package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
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

// TestUpsertTrinoQuerySnapshot_TwoWriters: the poller and the query events
// write the same row (#406). A poll read before the query ended does not set
// a terminal state back to running, and a figure the poller does not know
// (output rows) keeps the event's.
func TestUpsertTrinoQuerySnapshot_TwoWriters(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	var conn, engine pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url) VALUES ('tri', 'trino', 'http://t') RETURNING id`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO engines (name, kind, class, connection_id) VALUES ('trino', 'trino', 'federated', $1) RETURNING id`, conn).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	q := New(pool)
	rows := int64(10)
	cpu := int32(840)
	snap := func(state string, outputRows *int64) UpsertTrinoQuerySnapshotParams {
		return UpsertTrinoQuerySnapshotParams{
			TrinoQueryID: "q1", EngineID: engine, Origin: "trino_direct", User: "ada", State: state,
			Sql: "SELECT 1", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, CpuMs: &cpu, OutputRows: outputRows,
		}
	}

	if _, err := q.UpsertTrinoQuerySnapshot(ctx, snap("finished", &rows)); err != nil {
		t.Fatal(err)
	}
	got, err := q.UpsertTrinoQuerySnapshot(ctx, snap("running", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "finished" {
		t.Errorf("state = %q after a late poll, want finished", got.State)
	}
	if got.OutputRows == nil || *got.OutputRows != rows {
		t.Errorf("output_rows = %v after a poll without them, want the event's %d", got.OutputRows, rows)
	}

	failed, err := q.UpsertTrinoQuerySnapshot(ctx, snap("failed", nil))
	if err != nil || failed.State != "failed" {
		t.Errorf("a terminal state = %q, %v; want it to replace another terminal one", failed.State, err)
	}
}
