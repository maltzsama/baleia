package metadata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// testGORM opens the GORM store over a test pool, as the application does.
func testGORM(t *testing.T, pool *pgxpool.Pool) *gorm.DB {
	t.Helper()
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// testStore builds the GORM store over the test database.
func testStore(t *testing.T, pool *pgxpool.Pool) *repo.Repo {
	t.Helper()
	return repo.New(testGORM(t, pool))
}

// newSyncer builds the GORM-backed syncer over the test database.
func newSyncer(t *testing.T, pool *pgxpool.Pool, opener Opener) *Syncer {
	t.Helper()
	r := testStore(t, pool)
	return NewSyncer(r, func(ctx context.Context, runID pgtype.UUID, fn func(Store) error) error {
		return r.TxMetadata(ctx, runID, func(tx *repo.Repo) error { return fn(tx) })
	}, opener)
}

// testCatalog inserts a native catalog on a fresh connection.
func testCatalog(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	gdb := testGORM(t, pool)
	conn, err := repo.New(gdb).CreateCatalogConnection(context.Background(), db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "src", Type: "polaris", Url: "http://x",
	})
	if err != nil {
		t.Fatal(err)
	}
	cat := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(conn.ID)),
		Name: "lake", Origin: "native",
	}
	if err := gdb.Create(&cat).Error; err != nil {
		t.Fatal(err)
	}
	return repo.PGUUID(string(cat.ID))
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
	fks     []ForeignKeySnapshot
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
		t.ForeignKeys = ft.fks
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

func count(t *testing.T, q *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := q.Count(&n).Error; err != nil {
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
	gdb := testGORM(t, pool)
	src := &fakeSource{tables: twoByThree()}
	stats, err := newSyncer(t, pool, fakeOpener{src}).Sync(context.Background(), cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TablesSeen != 6 || stats.TablesChanged != 6 || stats.TablesRemoved != 0 || stats.Path != PathProvider {
		t.Fatalf("stats = %+v", stats)
	}
	if n := count(t, gdb.Model(&model.MetadataTable{}).Where("catalog_id = ? AND removed_at IS NULL", cat)); n != 6 {
		t.Fatalf("tables = %d", n)
	}
	if n := count(t, gdb.Model(&model.MetadataColumn{})); n != 12 {
		t.Fatalf("columns = %d", n)
	}
	if n := count(t, gdb.Model(&model.MetadataColumn{}).Where("type_system = ? AND native_type = ?", "iceberg", "long")); n != 12 {
		t.Fatalf("columns with type system = %d", n)
	}
	var namespaces []model.MetadataNamespace
	if err := gdb.Where("catalog_id = ?", cat).Find(&namespaces).Error; err != nil {
		t.Fatal(err)
	}
	var ns model.StringArray
	for _, n := range namespaces {
		if len(n.Namespace) == 2 {
			ns = n.Namespace
		}
	}
	if !slices.Equal(ns, []string{"ops", "eu"}) {
		t.Fatalf("multi-level namespace = %v", ns)
	}
	var run model.MetadataSyncRun
	if err := gdb.Where("catalog_id = ?", cat).First(&run).Error; err != nil || run.Status != "finished" || run.SourcePath != "provider" || run.TablesSeen == nil || *run.TablesSeen != 6 {
		t.Fatalf("run = %+v, %v", run, err)
	}
}

// TestSync_OnlyChangedTablesReload: a second pass with one changed version
// loads that table only and rewrites only its columns.
func TestSync_OnlyChangedTablesReload(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	gdb := testGORM(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := newSyncer(t, pool, fakeOpener{src})
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
	if n := count(t, gdb.Model(&model.MetadataColumn{}).
		Joins("JOIN metadata_tables t ON t.id = metadata_columns.table_id").
		Where("t.name = ? AND t.namespace = ?", "t1", model.StringArray{"ops", "eu"})); n != 3 {
		t.Fatalf("columns of the changed table = %d", n)
	}
	if n := count(t, gdb.Model(&model.MetadataColumn{})); n != 13 {
		t.Fatalf("columns = %d", n)
	}
}

// TestSync_ForeignKeys: a table's foreign keys are stored with it and replaced
// when the table changes and drops them (B1, #554).
func TestSync_ForeignKeys(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	fk := ForeignKeySnapshot{Column: "customer_id", RefNamespace: []string{"sales"}, RefTable: "customers", RefColumn: "id"}
	src := &fakeSource{tables: []fakeTable{
		{ns: []string{"sales"}, name: "orders", version: "v1", cols: []string{"id", "customer_id"}, fks: []ForeignKeySnapshot{fk}},
		{ns: []string{"sales"}, name: "customers", version: "v1", cols: []string{"id"}},
	}}
	s := newSyncer(t, pool, fakeOpener{src})
	ctx := context.Background()
	gdb := testGORM(t, pool)
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, gdb.Model(&model.MetadataForeignKey{}).Where("column_name = ? AND ref_table = ? AND ref_namespace = ?", "customer_id", "customers", model.StringArray{"sales"})); n != 1 {
		t.Fatalf("foreign keys = %d", n)
	}

	src.tables[0].version, src.tables[0].fks = "v2", nil
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, gdb.Model(&model.MetadataForeignKey{})); n != 0 {
		t.Fatalf("foreign keys after the drop = %d", n)
	}
}

// TestSync_Tombstones: a table missing from a pass is tombstoned (and its
// namespace when empty); when it reappears the tombstone is cleared.
func TestSync_Tombstones(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := newSyncer(t, pool, fakeOpener{src})
	ctx := context.Background()
	gdb := testGORM(t, pool)
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	gone := src.tables[0]
	src.tables = src.tables[1:]
	stats, err := s.Sync(ctx, cat, nil)
	if err != nil || stats.TablesRemoved != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if n := count(t, gdb.Model(&model.MetadataTable{}).
		Where("name = ? AND namespace = ? AND removed_at IS NOT NULL", gone.name, model.StringArray(gone.ns))); n != 1 {
		t.Fatal("the missing table must be tombstoned")
	}

	src.tables = append([]fakeTable{gone}, src.tables...)
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, gdb.Model(&model.MetadataTable{}).Where("removed_at IS NOT NULL")); n != 0 {
		t.Fatal("a table that reappears is no longer removed")
	}
	if n := count(t, gdb.Model(&model.MetadataColumn{}).
		Joins("JOIN metadata_tables t ON t.id = metadata_columns.table_id").
		Where("t.name = ? AND t.namespace = ?", gone.name, model.StringArray(gone.ns))); n != 2 {
		t.Fatalf("the reappeared table has its columns: %d", n)
	}

	// A namespace with no table left is tombstoned with its tables.
	src.tables = src.tables[:3]
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, gdb.Model(&model.MetadataNamespace{}).
		Where("namespace = ? AND removed_at IS NOT NULL", model.StringArray{"ops", "eu"})); n != 1 {
		t.Fatal("the emptied namespace must be tombstoned")
	}

	// Tombstones older than 7 days are purged by the next pass.
	if err := gdb.Model(&model.MetadataTable{}).Where("removed_at IS NOT NULL").
		Update("removed_at", time.Now().Add(-8*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, cat, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, gdb.Model(&model.MetadataTable{}).Where("catalog_id = ?", cat)); n != 3 {
		t.Fatalf("tables after the purge = %d", n)
	}
}

// TestSync_ConcurrentLocked: a second sync of a live pass waits for its lease
// and then gives up with ErrSyncInProgress, never running concurrently.
func TestSync_ConcurrentLocked(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	src := &fakeSource{tables: twoByThree(), block: make(chan struct{}), entered: make(chan struct{})}
	s := newSyncer(t, pool, fakeOpener{src})
	s.heartbeatEvery = 100 * time.Millisecond
	s.staleAfter = time.Second
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
	// The lease is released: the next sync runs.
	src.block, src.entered = nil, nil
	if _, err := s.Sync(context.Background(), cat, nil); err != nil {
		t.Fatal(err)
	}
}

// TestSync_StaleRunTakenOver: a running row with a stale heartbeat left by a
// crashed worker is failed and the catalog is taken over (ADR 0020).
func TestSync_StaleRunTakenOver(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	store := testStore(t, pool)
	ctx := context.Background()
	gdb := testGORM(t, pool)
	stale, err := store.StartMetadataSyncRun(ctx, db.StartMetadataSyncRunParams{CatalogID: cat, SourcePath: PathProvider})
	if err != nil {
		t.Fatal(err)
	}
	anHourAgo := time.Now().Add(-time.Hour)
	if err := gdb.Model(&model.MetadataSyncRun{}).Where("id = ?", repo.UUIDString(stale.ID)).
		Updates(map[string]any{"started_at": anHourAgo, "heartbeat_at": anHourAgo}).Error; err != nil {
		t.Fatal(err)
	}

	src := &fakeSource{tables: twoByThree()}
	stats, err := newSyncer(t, pool, fakeOpener{src}).Sync(ctx, cat, nil)
	if err != nil || stats.TablesSeen != 6 {
		t.Fatalf("sync = %+v, %v", stats, err)
	}
	var run model.MetadataSyncRun
	if err := gdb.Where("id = ?", repo.UUIDString(stale.ID)).First(&run).Error; err != nil || run.Status != "failed" || run.ErrorCode == nil || *run.ErrorCode != "interrupted" {
		t.Fatalf("stale run = %+v, %v", run, err)
	}
	if n := count(t, gdb.Model(&model.MetadataSyncRun{}).Where("catalog_id = ? AND status = ?", cat, "running")); n != 0 {
		t.Fatalf("running rows = %d", n)
	}
}

// TestSync_FreshRunLeaseWaitedOut: a retry that finds a fresh heartbeat must
// wait for the lease to expire instead of failing at once, then take over.
func TestSync_FreshRunLeaseWaitedOut(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	store := testStore(t, pool)
	ctx := context.Background()
	gdb := testGORM(t, pool)
	crashed, err := store.StartMetadataSyncRun(ctx, db.StartMetadataSyncRunParams{CatalogID: cat, SourcePath: PathProvider})
	if err != nil {
		t.Fatal(err)
	}
	// The row of a worker that just died: its heartbeat is still fresh.
	if err := gdb.Model(&model.MetadataSyncRun{}).Where("id = ?", repo.UUIDString(crashed.ID)).
		Update("heartbeat_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}

	s := newSyncer(t, pool, fakeOpener{&fakeSource{tables: twoByThree()}})
	s.heartbeatEvery = 100 * time.Millisecond
	s.staleAfter = time.Second
	start := time.Now()
	stats, err := s.Sync(ctx, cat, nil)
	if err != nil || stats.TablesSeen != 6 {
		t.Fatalf("sync = %+v, %v", stats, err)
	}
	if elapsed := time.Since(start); elapsed < s.staleAfter {
		t.Fatalf("retry took %s, want it to wait for the %s lease", elapsed, s.staleAfter)
	}
	var run model.MetadataSyncRun
	if err := gdb.Where("id = ?", repo.UUIDString(crashed.ID)).First(&run).Error; err != nil || run.Status != "failed" {
		t.Fatalf("crashed run = %+v, %v", run, err)
	}
}

// TestSync_FailedRunRecorded: a source error fails the run with a code and
// keeps what was stored (nothing is tombstoned by a partial pass).
func TestSync_FailedRunRecorded(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	cat := testCatalog(t, pool)
	gdb := testGORM(t, pool)
	src := &fakeSource{tables: twoByThree()}
	s := newSyncer(t, pool, fakeOpener{src})
	if _, err := s.Sync(context.Background(), cat, nil); err != nil {
		t.Fatal(err)
	}
	src.tables, src.err = src.tables[:1], errors.New("catalog manager down")
	if _, err := s.Sync(context.Background(), cat, nil); err == nil {
		t.Fatal("the source error must fail the sync")
	}
	var run model.MetadataSyncRun
	if err := gdb.Where("catalog_id = ?", cat).Order("started_at DESC").First(&run).Error; err != nil || run.Status != "failed" || run.ErrorCode == nil || *run.ErrorCode != "sync_failed" {
		t.Fatalf("run = %+v, %v", run, err)
	}
	if n := count(t, gdb.Model(&model.MetadataTable{}).Where("removed_at IS NOT NULL")); n != 0 {
		t.Fatal("a failed pass must not tombstone")
	}
}

func TestSync_UnknownCatalog(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	_, err := newSyncer(t, pool, fakeOpener{&fakeSource{}}).Sync(context.Background(), pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, nil)
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
