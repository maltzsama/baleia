package metadata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func testPool(t *testing.T) *pgxpool.Pool { return testdb.New(t) }

// testCatalog inserts a native catalog on a fresh connection.
func testCatalog(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var conn, cat pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url) VALUES ('src', 'polaris', 'http://x') RETURNING id`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'lake', 'native') RETURNING id`, conn).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	return cat
}

// fakeSource is an Iceberg-like source: a fixed set of tables whose
// fingerprint is their version; it "loads" a table only when the stored
// fingerprint differs.
type fakeSource struct {
	mu      sync.Mutex
	tables  []fakeTable
	loads   int
	block   chan struct{} // when set, Walk waits on it
	entered chan struct{}
	err     error
}

type fakeTable struct {
	ns      []string
	name    string
	version string
	cols    []string
}

func (s *fakeSource) TypeSystem() string { return "iceberg" }
func (s *fakeSource) Close() error       { return nil }

func (s *fakeSource) Walk(ctx context.Context, known func([]string, string) (Known, bool), v Visitor) error {
	if s.entered != nil {
		close(s.entered)
	}
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	tables := slices.Clone(s.tables)
	s.mu.Unlock()
	for _, ft := range tables {
		if err := v.Namespace(ft.ns); err != nil {
			return err
		}
		if k, ok := known(ft.ns, ft.name); ok && k.Fingerprint == ft.version {
			if err := v.Table(TableSnapshot{Namespace: ft.ns, Name: ft.name, Fingerprint: ft.version, Unchanged: true}); err != nil {
				return err
			}
			continue
		}
		s.mu.Lock()
		s.loads++
		s.mu.Unlock()
		t := TableSnapshot{Namespace: ft.ns, Name: ft.name, Kind: KindTable, Fingerprint: ft.version, Properties: map[string]string{"format": "iceberg"}}
		for i, c := range ft.cols {
			t.Columns = append(t.Columns, ColumnSnapshot{Ordinal: i + 1, Name: c, NativeType: "long", Nullable: true})
		}
		if err := v.Table(t); err != nil {
			return err
		}
	}
	return s.err
}

type fakeOpener struct{ src Source }

func (o fakeOpener) Open(context.Context, db.GetCatalogForSyncRow) (string, Source, error) {
	return PathProvider, o.src, nil
}

func twoByThree() []fakeTable {
	var out []fakeTable
	for _, ns := range [][]string{{"sales"}, {"ops", "eu"}} {
		for i := range 3 {
			out = append(out, fakeTable{ns: ns, name: fmt.Sprintf("t%d", i), version: "v1", cols: []string{"id", "amount"}})
		}
	}
	return out
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSync_InitialPass: 2 namespaces × 3 tables are stored with their
// columns and a finished run; multi-level namespaces round-trip.
func TestSync_InitialPass(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree()}
	stats, err := NewSyncer(pool, fakeOpener{src}).Sync(context.Background(), cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TablesSeen != 6 || stats.TablesChanged != 6 || stats.TablesRemoved != 0 || stats.Path != PathProvider {
		t.Fatalf("stats = %+v", stats)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_tables WHERE catalog_id = $1 AND removed_at IS NULL`, cat); n != 6 {
		t.Fatalf("tables = %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_columns`); n != 12 {
		t.Fatalf("columns = %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_columns WHERE type_system = 'iceberg' AND native_type = 'long'`); n != 12 {
		t.Fatalf("columns with type system = %d", n)
	}
	var ns []string
	if err := pool.QueryRow(context.Background(), `SELECT namespace FROM metadata_namespaces WHERE catalog_id = $1 AND cardinality(namespace) = 2`, cat).Scan(&ns); err != nil || !slices.Equal(ns, []string{"ops", "eu"}) {
		t.Fatalf("multi-level namespace = %v, %v", ns, err)
	}
	var status, path string
	var seen int
	if err := pool.QueryRow(context.Background(), `SELECT status, source_path, tables_seen FROM metadata_sync_runs WHERE catalog_id = $1`, cat).Scan(&status, &path, &seen); err != nil || status != "finished" || path != "provider" || seen != 6 {
		t.Fatalf("run = %s %s %d, %v", status, path, seen, err)
	}
}

// TestSync_OnlyChangedTablesReload: a second pass with one changed version
// loads that table only and rewrites only its columns.
func TestSync_OnlyChangedTablesReload(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := NewSyncer(pool, fakeOpener{src})
	if _, err := s.Sync(context.Background(), cat, nil); err != nil {
		t.Fatal(err)
	}

	src.loads = 0
	stats, err := s.Sync(context.Background(), cat, nil)
	if err != nil || src.loads != 0 || stats.TablesChanged != 0 || stats.TablesSeen != 6 {
		t.Fatalf("unchanged re-sync: loads=%d stats=%+v err=%v", src.loads, stats, err)
	}

	src.tables[4].version, src.tables[4].cols = "v2", []string{"id", "amount", "note"}
	stats, err = s.Sync(context.Background(), cat, nil)
	if err != nil || src.loads != 1 || stats.TablesChanged != 1 {
		t.Fatalf("one changed: loads=%d stats=%+v err=%v", src.loads, stats, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_columns c JOIN metadata_tables t ON t.id = c.table_id WHERE t.name = 't1' AND t.namespace = '{ops,eu}'`); n != 3 {
		t.Fatalf("columns of the changed table = %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_columns`); n != 13 {
		t.Fatalf("columns = %d", n)
	}
}

// TestSync_Tombstones: a table missing from a pass is tombstoned (and its
// namespace when empty); when it reappears the tombstone is cleared.
func TestSync_Tombstones(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := NewSyncer(pool, fakeOpener{src})
	ctx := context.Background()
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	gone := src.tables[0]
	src.tables = src.tables[1:]
	stats, err := s.Sync(ctx, cat, nil)
	if err != nil || stats.TablesRemoved != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_tables WHERE name = $1 AND namespace = $2 AND removed_at IS NOT NULL`, gone.name, gone.ns); n != 1 {
		t.Fatal("the missing table must be tombstoned")
	}

	src.tables = append([]fakeTable{gone}, src.tables...)
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_tables WHERE removed_at IS NOT NULL`); n != 0 {
		t.Fatal("a table that reappears is no longer removed")
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_columns c JOIN metadata_tables t ON t.id = c.table_id WHERE t.name = $1 AND t.namespace = $2`, gone.name, gone.ns); n != 2 {
		t.Fatalf("the reappeared table has its columns: %d", n)
	}

	// A namespace with no table left is tombstoned with its tables.
	src.tables = src.tables[:3]
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_namespaces WHERE namespace = '{ops,eu}' AND removed_at IS NOT NULL`); n != 1 {
		t.Fatal("the emptied namespace must be tombstoned")
	}

	// Tombstones older than 7 days are purged by the next pass.
	if _, err := pool.Exec(ctx, `UPDATE metadata_tables SET removed_at = now() - interval '8 days' WHERE removed_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_tables WHERE catalog_id = $1`, cat); n != 3 {
		t.Fatalf("tables after the purge = %d", n)
	}
}

// TestSync_ConcurrentLocked: a second sync of the same catalog while one runs
// gets ErrSyncInProgress.
func TestSync_ConcurrentLocked(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree(), block: make(chan struct{}), entered: make(chan struct{})}
	s := NewSyncer(pool, fakeOpener{src})
	done := make(chan error, 1)
	go func() {
		_, err := s.Sync(context.Background(), cat, nil)
		done <- err
	}()
	<-src.entered
	if _, err := s.Sync(context.Background(), cat, nil); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("second sync: %v, want ErrSyncInProgress", err)
	}
	close(src.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The lock is released: the next sync runs.
	src.block, src.entered = nil, nil
	if _, err := s.Sync(context.Background(), cat, nil); err != nil {
		t.Fatal(err)
	}
}

// TestSync_FailedRunRecorded: a source error fails the run with a code and
// keeps what was stored (nothing is tombstoned by a partial pass).
func TestSync_FailedRunRecorded(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := NewSyncer(pool, fakeOpener{src})
	if _, err := s.Sync(context.Background(), cat, nil); err != nil {
		t.Fatal(err)
	}
	src.tables, src.err = src.tables[:1], errors.New("catalog manager down")
	if _, err := s.Sync(context.Background(), cat, nil); err == nil {
		t.Fatal("the source error must fail the sync")
	}
	var status, code string
	if err := pool.QueryRow(context.Background(), `SELECT status, error_code FROM metadata_sync_runs WHERE catalog_id = $1 ORDER BY started_at DESC LIMIT 1`, cat).Scan(&status, &code); err != nil || status != "failed" || code != "sync_failed" {
		t.Fatalf("run = %s %s, %v", status, code, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM metadata_tables WHERE removed_at IS NOT NULL`); n != 0 {
		t.Fatal("a failed pass must not tombstone")
	}
}

func TestSync_UnknownCatalog(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	_, err := NewSyncer(pool, fakeOpener{&fakeSource{}}).Sync(context.Background(), pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, nil)
	if !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestTableKeyDistinguishesLevels(t *testing.T) {
	t.Parallel()
	if tableKey([]string{"a", "b"}, "c") == tableKey([]string{"a"}, "b\x1fc") || strings.Contains(tableKey(nil, "t"), "\x1f") {
		t.Fatal("table keys must not collide across namespace levels")
	}
}
