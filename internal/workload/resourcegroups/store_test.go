package resourcegroups

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/galileostudio/baleia/internal/workload"
	"gorm.io/gorm"
)

// TestStore_WriteAndRollback publishes a document, changes it, then rolls back
// to the first revision and verifies the tables reflect it again (#599).
func TestStore_WriteAndRollback(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	engineID := insertEngine(t, gdb)

	store := New(gdb)

	doc := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{}, nil)
	rev1, err := store.Write(ctx, engineID, "production", doc)
	if err != nil {
		t.Fatalf("first write = %v", err)
	}
	if rev1 != 1 {
		t.Fatalf("first revision = %d, want 1", rev1)
	}
	if got := groupNames(t, gdb); len(got) != 4 {
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
	if got := groupNames(t, gdb); len(got) != 4 {
		t.Fatalf("groups after second write = %v, want root + 3 subgroups", got)
	}
	if got := groupLimit(t, gdb, "background"); got != 0 {
		t.Fatalf("closed background limit = %d, want 0", got)
	}

	// Roll back to the first revision.
	if err := store.Rollback(ctx, engineID, "production", rev1); err != nil {
		t.Fatalf("rollback = %v", err)
	}
	if got := groupLimit(t, gdb, "background"); got == 0 {
		t.Fatal("rollback left background closed")
	}
}

// TestStore_RollbackUnknownRevision reports ErrRevisionNotFound.
func TestStore_RollbackUnknownRevision(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	engineID := insertEngine(t, gdb)

	store := New(gdb)
	if err := store.Rollback(ctx, engineID, "production", 99); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("rollback = %v, want ErrRevisionNotFound", err)
	}
}

// TestStore_WriteInvalid rejects a document that fails Validate.
func TestStore_WriteInvalid(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	engineID := insertEngine(t, gdb)

	store := New(gdb)
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
	gdb := pool
	engineID := insertEngine(t, gdb)

	huge := workload.Capacity{MaxRunning: math.MaxInt32, MaxQueued: math.MaxInt32}
	doc := workload.Generate(map[queryengine.WorkloadClass]workload.Capacity{
		queryengine.ClassInteractive: huge,
		queryengine.ClassHeavy:       huge,
		queryengine.ClassBackground:  huge,
	}, nil)
	if _, err := New(gdb).Write(ctx, engineID, "production", doc); err != nil {
		t.Fatalf("write = %v", err)
	}
	var root model.ResourceGroup
	if err := gdb.Where("name = ?", "baleia").First(&root).Error; err != nil {
		t.Fatal(err)
	}
	maxQueued := int64(root.MaxQueued)
	limit := int64(root.HardConcurrencyLimit)
	if maxQueued != math.MaxInt32 || limit != math.MaxInt32 {
		t.Fatalf("root max_queued = %d, hard_concurrency_limit = %d, want both %d", maxQueued, limit, math.MaxInt32)
	}
}

// groupLimit is a group's hard concurrency limit in the production environment.
func groupLimit(t *testing.T, gdb *gorm.DB, name string) int64 {
	t.Helper()
	var group model.ResourceGroup
	if err := gdb.Where("environment = ? AND name = ?", "production", name).First(&group).Error; err != nil {
		t.Fatal(err)
	}
	return int64(group.HardConcurrencyLimit)
}

// groupNames lists the resource group names stored for the environment.
func groupNames(t *testing.T, gdb *gorm.DB) []string {
	t.Helper()
	var groups []model.ResourceGroup
	if err := gdb.Where("environment = ?", "production").Order("name").Find(&groups).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.Name
	}
	return out
}

// insertEngine creates a connection and a Trino engine for the revision FK.
func insertEngine(t *testing.T, gdb *gorm.DB) db.UUID {
	t.Helper()
	ctx := context.Background()
	q := repo.New(gdb)
	conn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "trino-conn", Type: "trino", Url: "http://trino",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name: "trino", Kind: "trino", Class: "federated", ConnectionID: conn.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine.ID
}
