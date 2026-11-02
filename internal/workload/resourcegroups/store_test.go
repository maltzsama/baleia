package resourcegroups

import (
	"context"
	"errors"
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

	store := New(pool)

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

	// A second write with a closed background class leaves two groups.
	doc2 := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
		queryengine.ClassHeavy:       {MaxRunning: 1, MaxQueued: 10},
		queryengine.ClassBackground:  {MaxRunning: 0, MaxQueued: 0},
	}, nil)
	if _, err := store.Write(ctx, engineID, "production", doc2); err != nil {
		t.Fatalf("second write = %v", err)
	}
	if got := groupNames(t, pool); len(got) != 3 {
		t.Fatalf("groups after second write = %v, want root + 2 subgroups", got)
	}

	// Roll back to the first revision.
	if err := store.Rollback(ctx, engineID, "production", rev1); err != nil {
		t.Fatalf("rollback = %v", err)
	}
	if got := groupNames(t, pool); len(got) != 4 {
		t.Fatalf("groups after rollback = %v, want root + 3 subgroups", got)
	}
}

// TestStore_RollbackUnknownRevision reports ErrRevisionNotFound.
func TestStore_RollbackUnknownRevision(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	engineID := insertEngine(t, pool)

	store := New(pool)
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

	store := New(pool)
	_, err := store.Write(ctx, engineID, "production", workload.ResourceGroups{})
	if err == nil {
		t.Fatal("Write accepted an invalid document")
	}
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
