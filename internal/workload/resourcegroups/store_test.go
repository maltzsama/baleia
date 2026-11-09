package resourcegroups

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStore_WriteAndRollback publishes a document, changes it, then rolls back
// to the first revision and verifies the tables reflect it again (#599).
func TestStore_WriteAndRollback(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	engineID := insertEngine(t, pool)

	store := New(testdb.GORM(t, pool))

	doc := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{}, nil)
	rev1, err := store.Write(ctx, engineID, "production", doc)
	if err != nil {
		t.Fatalf("first write = %v", err)
	}
	if rev1 != 1 {
		t.Fatalf("first revision = %d, want 1", rev1)
	}
	if got := groupNames(t, pool); len(got) != 4 {
		t.Fatalf("groups after first write = %v, want root + 3 subgroups", got)
	}

	// A second write with a closed background class keeps its group, closed.
	doc2 := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
		queryengine.ClassHeavy:       {MaxRunning: 1, MaxQueued: 10},
		queryengine.ClassBackground:  {MaxRunning: 0, MaxQueued: 0},
	}, nil)
	if _, err := store.Write(ctx, engineID, "production", doc2); err != nil {
		t.Fatalf("second write = %v", err)
	}
	if got := groupNames(t, pool); len(got) != 4 {
		t.Fatalf("groups after second write = %v, want root + 3 subgroups", got)
	}
	if got := groupLimit(t, pool, "background"); got != 0 {
		t.Fatalf("closed background limit = %d, want 0", got)
	}

	// Roll back to the first revision.
	if err := store.Rollback(ctx, engineID, "production", rev1); err != nil {
		t.Fatalf("rollback = %v", err)
	}
	if got := groupLimit(t, pool, "background"); got == 0 {
		t.Fatal("rollback left background closed")
	}
}

// TestStore_RollbackUnknownRevision reports ErrRevisionNotFound.
func TestStore_RollbackUnknownRevision(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	engineID := insertEngine(t, pool)

	store := New(testdb.GORM(t, pool))
	if err := store.Rollback(ctx, engineID, "production", 99); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("rollback = %v, want ErrRevisionNotFound", err)
	}
}

// TestStore_WriteInvalid rejects a document that fails Validate.
func TestStore_WriteInvalid(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	engineID := insertEngine(t, pool)

	store := New(testdb.GORM(t, pool))
	_, err := store.Write(ctx, engineID, "production", workload.ResourceGroups{})
	if err == nil {
		t.Fatal("Write accepted an invalid document")
	}
}

// TestStore_WriteClampsRootSums: the root's derived limits are sums of the
// class limits and are clamped to the int32 columns instead of wrapping (#635).
func TestStore_WriteClampsRootSums(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	engineID := insertEngine(t, pool)

	huge := workload.Capacity{MaxRunning: math.MaxInt32, MaxQueued: math.MaxInt32}
	doc := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{
		queryengine.ClassInteractive: huge,
		queryengine.ClassHeavy:       huge,
		queryengine.ClassBackground:  huge,
	}, nil)
	if _, err := New(testdb.GORM(t, pool)).Write(ctx, engineID, "production", doc); err != nil {
		t.Fatalf("write = %v", err)
	}
	var maxQueued, limit int64
	if err := pool.QueryRow(ctx, `SELECT max_queued, hard_concurrency_limit FROM resource_groups WHERE name = 'baleia'`).Scan(&maxQueued, &limit); err != nil {
		t.Fatal(err)
	}
	if maxQueued != math.MaxInt32 || limit != math.MaxInt32 {
		t.Fatalf("root max_queued = %d, hard_concurrency_limit = %d, want both %d", maxQueued, limit, math.MaxInt32)
	}
}

// groupLimit is a group's hard concurrency limit in the production environment.
func groupLimit(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var limit int64
	if err := pool.QueryRow(context.Background(), `SELECT hard_concurrency_limit FROM resource_groups WHERE environment = 'production' AND name = $1`, name).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	return limit
}

// groupNames lists the resource group names stored for the environment.
func groupNames(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT name FROM resource_groups WHERE environment = 'production' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

// insertEngine creates a connection and a Trino engine for the revision FK.
func insertEngine(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var conn pgtype.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO catalog_connections (name, type, url) VALUES ('trino-conn', 'trino', 'http://trino') RETURNING id`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	var engine pgtype.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO engines (name, kind, class, connection_id) VALUES ('trino', 'trino', 'federated', $1) RETURNING id`, conn).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	return engine
}
