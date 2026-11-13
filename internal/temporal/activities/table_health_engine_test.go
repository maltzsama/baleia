package activities

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/glue"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// glueWithTables is the Glue provider with a canned Data Catalog: the engine
// path only needs its tables and their formats.
type glueWithTables struct {
	*glue.Provider
	tables []connections.TableMeta
}

func (g glueWithTables) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return &cannedGlueReader{tables: g.tables}, nil
}

type cannedGlueReader struct {
	connections.MetadataReader
	tables []connections.TableMeta
}

func (*cannedGlueReader) Close() error { return nil }

func (*cannedGlueReader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: glue.Catalog, Kind: "glue"}}, nil
}

func (r *cannedGlueReader) WalkCatalog(_ context.Context, _ string, visit func(connections.TableMeta) error) error {
	for _, t := range r.tables {
		if err := visit(t); err != nil {
			return err
		}
	}
	return nil
}

func glueTable(db, name, format string) connections.TableMeta {
	return connections.TableMeta{
		Ref:    connections.TableRef{Catalog: glue.Catalog, Namespace: connections.Namespace{db}, Name: name, Type: "TABLE"},
		Format: format,
	}
}

// metadataTables is what the engine's metadata tables hold for one table.
type metadataTables struct {
	properties [][2]string
	current    []string // snapshot id, summary JSON; nil for no snapshot
	stats      []string // count, oldest ms, commits in the hour, in the day
	fail       bool
}

// metadataEngine answers the health reads from canned metadata tables and
// records every session it opens. It embeds the Trino adapter, so the SQL
// under test is Trino's own.
type metadataEngine struct {
	*qtrino.Engine
	tables map[string]metadataTables
	mu     sync.Mutex
	specs  []queryengine.SessionSpec
	sqls   []string
}

func (e *metadataEngine) Get(context.Context, db.UUID) (queryengine.Engine, error) { return e, nil }

func (e *metadataEngine) DefaultTrino(context.Context) (queryengine.Engine, error) { return e, nil }

func (e *metadataEngine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	e.mu.Lock()
	e.specs = append(e.specs, spec)
	e.mu.Unlock()
	return &metadataSession{e: e}, nil
}

type metadataSession struct{ e *metadataEngine }

func (*metadataSession) Close() error { return nil }

func (s *metadataSession) Execute(_ context.Context, sql string, _ queryengine.Callbacks, sink queryengine.RecordSink) error {
	s.e.mu.Lock()
	s.e.sqls = append(s.e.sqls, sql)
	s.e.mu.Unlock()
	for name, t := range s.e.tables {
		if !strings.Contains(sql, `"`+name+`$`) {
			continue
		}
		if t.fail {
			return queryengine.NewError(queryengine.CodeNotFound, "fake", "", "table not found")
		}
		switch {
		case strings.Contains(sql, "$properties"):
			rows := make([][]any, len(t.properties))
			for i, p := range t.properties {
				rows[i] = []any{p[0], p[1]}
			}
			return emitStrings(sink, 2, rows)
		case strings.Contains(sql, "$refs"):
			if t.current == nil {
				return emitStrings(sink, 2, nil)
			}
			return emitStrings(sink, 2, [][]any{{t.current[0], t.current[1]}})
		case strings.Contains(sql, "$snapshots"):
			row := make([]any, len(t.stats))
			for i, v := range t.stats {
				if v == "" {
					row[i] = nil
				} else {
					row[i] = v
				}
			}
			return emitStrings(sink, 4, [][]any{row})
		}
	}
	return queryengine.NewError(queryengine.CodeSyntax, "fake", "", "unexpected statement "+sql)
}

func emitStrings(sink queryengine.RecordSink, cols int, rows [][]any) error {
	info := make([]arrowipc.ColumnInfo, cols)
	for i := range info {
		info[i] = arrowipc.ColumnInfo{Name: "c" + strconv.Itoa(i), Type: "varchar"}
	}
	b, err := arrowipc.NewBatcher(info)
	if err != nil {
		return err
	}
	defer b.Release()
	if err := sink.Begin(b.Schema()); err != nil {
		return err
	}
	if err := b.Append(rows, sink.Write); err != nil {
		return err
	}
	if err := b.Flush(sink.Write); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{RowCount: int64(len(rows))})
}

// glueHealthFixture seeds a Glue connection whose catalog is bound with
// connector on a Trino engine, and the activities that read it.
func glueHealthFixture(t *testing.T, connector string, tables []connections.TableMeta, eng *metadataEngine, cfg *config.Config) (*Activities, *repo.Repo, LensAnalyzeTableHealthParams, db.UUID) {
	t.Helper()
	gdb := testdb.New(t)
	store := repo.New(gdb)
	user := seedUser(t, gdb, "glue", "glue@health.test")
	conn := seedConnection(t, gdb, "glue-src", user)
	engine := seedEngine(t, gdb, "glue-e", seedConnection(t, gdb, "glue-eng", user), user)
	catalog, _ := seedCatalogBinding(t, gdb, conn, engine, "glue_iceberg")
	if err := gdb.Model(&model.CatalogConnection{}).Where("id = ?", repo.UUIDString(conn)).
		Updates(map[string]any{"type": "glue", "config": []byte(`{"region":"us-east-1"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.Catalog{}).Where("id = ?", repo.UUIDString(catalog)).
		Update("connector", connector).Error; err != nil {
		t.Fatal(err)
	}
	dialer, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := connections.NewRegistry(glueWithTables{Provider: glue.New(dialer, glue.Options{}), tables: tables})
	if err != nil {
		t.Fatal(err)
	}
	eng.Engine = testTrino
	a := &Activities{store: store, engines: eng, connections: reg, logger: slog.New(slog.DiscardHandler), cfg: cfg}
	params := LensAnalyzeTableHealthParams{
		CatalogID: repo.UUIDString(catalog), CatalogConnectionID: repo.UUIDString(conn), Catalog: "wh",
	}
	return a, store, params, catalog
}

// TestEngineTableHealth: a Glue catalog bound with the Iceberg connector gets
// health for its Iceberg tables through the engine's metadata tables, scored
// like a catalog read; its Hive and Delta Lake tables get none, and every read
// is a read-only session of the service identity (#997).
func TestEngineTableHealth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldest := time.Now().Add(-48 * time.Hour).UnixMilli()
	summary, _ := json.Marshal(map[string]string{
		"total-data-files": "120", "total-files-size": "1200", "total-delete-files": "3",
		"total-equality-deletes": "4", "total-position-deletes": "5",
	})
	eng := &metadataEngine{tables: map[string]metadataTables{
		"orders": {
			properties: [][2]string{
				{"format", "iceberg/PARQUET"}, {"provider", "iceberg"}, {"current-snapshot-id", "42"},
				{"location", "s3://lake/sales/orders"}, {"format-version", "2"}, {"created-at", "2026-01-01T00:00:00Z"},
				{"write.parquet.compression-codec", "zstd"},
			},
			current: []string{"42", string(summary)},
			stats:   []string{"60", strconv.FormatInt(oldest, 10), "2", "7"},
		},
		"empty": {
			properties: [][2]string{{"format-version", "1"}},
			stats:      []string{"0", "", "0", "0"},
		},
	}}
	tables := []connections.TableMeta{
		glueTable("sales", "orders", "iceberg"),
		glueTable("sales", "legacy", "hive"),
		glueTable("sales", "events", "delta"),
		glueTable("sales", "empty", "iceberg"),
	}
	a, store, params, catalog := glueHealthFixture(t, "iceberg", tables, eng, &config.Config{})

	if err := a.lensAnalyzeTableHealth(ctx, params); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	got, err := store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{CatalogID: catalog, Namespace: "sales", TableName: "orders"})
	if err != nil {
		t.Fatalf("orders health: %v", err)
	}
	if got.SnapshotCount != 60 || *got.FileCount != 120 || *got.TotalSizeBytes != 1200 || *got.AvgFileSizeBytes != 10 {
		t.Errorf("orders counts = snapshots %d, files %d, size %d, avg %d", got.SnapshotCount, *got.FileCount, *got.TotalSizeBytes, *got.AvgFileSizeBytes)
	}
	if *got.DeleteFileCount != 3 || *got.EqualityDeleteCount != 4 || *got.PositionDeleteCount != 5 || *got.FormatVersion != 2 {
		t.Errorf("orders deletes = %d/%d/%d, format v%d", *got.DeleteFileCount, *got.EqualityDeleteCount, *got.PositionDeleteCount, *got.FormatVersion)
	}
	if *got.CommitsLastHour != 2 || *got.CommitsLastDay != 7 || *got.OldestSnapshotAgeHours < 47 || *got.OldestSnapshotAgeHours > 48 {
		t.Errorf("orders log = hour %d, day %d, oldest %dh", *got.CommitsLastHour, *got.CommitsLastDay, *got.OldestSnapshotAgeHours)
	}
	// The default thresholds: more than 50 snapshots and 100 data files.
	if got.HealthScore != 40 || !got.NeedsCompaction || !got.NeedsSnapshotExpiry {
		t.Errorf("orders score = %d, compaction %v, expiry %v", got.HealthScore, got.NeedsCompaction, got.NeedsSnapshotExpiry)
	}
	var props map[string]string
	if err := json.Unmarshal(got.TableProperties, &props); err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props["write.parquet.compression-codec"] != "zstd" {
		t.Errorf("orders properties = %v, want only the table's own", props)
	}

	empty, err := store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{CatalogID: catalog, Namespace: "sales", TableName: "empty"})
	if err != nil {
		t.Fatalf("empty health: %v", err)
	}
	if empty.SnapshotCount != 0 || empty.FileCount != nil || empty.CommitsLastDay != nil || empty.OldestSnapshotAgeHours != nil || *empty.FormatVersion != 1 {
		t.Errorf("empty table = %+v", empty)
	}

	for _, name := range []string{"legacy", "events"} {
		if _, err := store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{CatalogID: catalog, Namespace: "sales", TableName: name}); !errors.Is(err, db.ErrNoRows) {
			t.Errorf("%s: health recorded for a non-Iceberg table (err %v)", name, err)
		}
	}
	for _, sql := range eng.sqls {
		if strings.Contains(sql, "legacy") || strings.Contains(sql, "events") {
			t.Errorf("the engine read a non-Iceberg table: %s", sql)
		}
		if !strings.Contains(sql, `"glue_iceberg"."sales".`) {
			t.Errorf("read outside the binding's engine catalog: %s", sql)
		}
	}
	if len(eng.specs) == 0 {
		t.Fatal("no engine session opened")
	}
	for _, spec := range eng.specs {
		if !spec.ReadOnly || spec.Identity.UserID != "" || spec.Tags.Source != "table_health" {
			t.Errorf("session = read-only %v, user %q, source %q; want a read-only service session", spec.ReadOnly, spec.Identity.UserID, spec.Tags.Source)
		}
	}
}

// TestEngineTableHealth_Limit: a pass reads at most the configured number of
// tables; the others are skipped and the pass still succeeds.
func TestEngineTableHealth_Limit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stats := metadataTables{stats: []string{"0", "", "0", "0"}}
	eng := &metadataEngine{tables: map[string]metadataTables{"a": stats, "b": stats, "c": stats}}
	tables := []connections.TableMeta{glueTable("s", "a", "iceberg"), glueTable("s", "b", "iceberg"), glueTable("s", "c", "iceberg")}
	a, store, params, catalog := glueHealthFixture(t, "iceberg", tables, eng, &config.Config{TableHealthMaxEngineTables: 2})

	if err := a.lensAnalyzeTableHealth(ctx, params); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for name, want := range map[string]bool{"a": true, "b": true, "c": false} {
		_, err := store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{CatalogID: catalog, Namespace: "s", TableName: name})
		if recorded := err == nil; recorded != want {
			t.Errorf("table %s recorded = %v, want %v (err %v)", name, recorded, want, err)
		}
	}
}

// TestEngineTableHealth_Failures: a table the engine cannot read is skipped,
// the pass fails only when it recorded nothing, and a Glue catalog bound with
// another connector is refused before the engine is asked (#750, #997).
func TestEngineTableHealth_Failures(t *testing.T) {
	t.Parallel()

	t.Run("one table fails", func(t *testing.T) {
		t.Parallel()
		eng := &metadataEngine{tables: map[string]metadataTables{
			"ok":     {stats: []string{"0", "", "0", "0"}},
			"broken": {fail: true},
		}}
		tables := []connections.TableMeta{glueTable("s", "broken", "iceberg"), glueTable("s", "ok", "iceberg")}
		a, store, params, catalog := glueHealthFixture(t, "iceberg", tables, eng, &config.Config{})
		ctx := t.Context()
		if err := a.lensAnalyzeTableHealth(ctx, params); err != nil {
			t.Fatalf("a partial pass failed: %v", err)
		}
		if _, err := store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{CatalogID: catalog, Namespace: "s", TableName: "ok"}); err != nil {
			t.Errorf("the readable table was not recorded: %v", err)
		}
	})

	t.Run("every table fails", func(t *testing.T) {
		t.Parallel()
		eng := &metadataEngine{tables: map[string]metadataTables{"broken": {fail: true}}}
		a, _, params, _ := glueHealthFixture(t, "iceberg", []connections.TableMeta{glueTable("s", "broken", "iceberg")}, eng, &config.Config{})
		ctx := t.Context()
		if err := a.lensAnalyzeTableHealth(ctx, params); err == nil {
			t.Fatal("a pass that recorded nothing succeeded")
		}
	})

	t.Run("hive connector", func(t *testing.T) {
		t.Parallel()
		eng := &metadataEngine{tables: map[string]metadataTables{}}
		a, _, params, _ := glueHealthFixture(t, "hive", []connections.TableMeta{glueTable("s", "t", "iceberg")}, eng, &config.Config{})
		ctx := t.Context()
		if err := a.lensAnalyzeTableHealth(ctx, params); !errors.Is(err, connections.ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
		if len(eng.specs) != 0 {
			t.Fatalf("the engine was asked %d times", len(eng.specs))
		}
	})
}

// TestTableHealthHeartbeats: the activity keeps heartbeating while a table's
// metadata read blocks, which can outlast the workflow's heartbeat timeout,
// and stops when the pass ends. Not parallel: it swaps package variables.
func TestTableHealthHeartbeats(t *testing.T) {
	intervalBefore := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	defer func() { heartbeatInterval = intervalBefore }()
	var beats atomic.Int64
	var last atomic.Int64
	recordBefore := recordHeartbeat
	defer func() { recordHeartbeat = recordBefore }()
	recordHeartbeat = func(_ context.Context, details ...any) {
		beats.Add(1)
		last.Store(details[0].(int64))
	}

	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	var afterStop int64
	env.RegisterActivityWithOptions(func(ctx context.Context) error {
		var read atomic.Int64
		read.Store(3)
		stop := tableHealthHeartbeats(ctx, &read)
		time.Sleep(200 * time.Millisecond) // one blocked read
		stop()
		afterStop = beats.Load()
		time.Sleep(50 * time.Millisecond)
		return nil
	}, activity.RegisterOptions{Name: "blocked"})
	if _, err := env.ExecuteActivity("blocked"); err != nil {
		t.Fatal(err)
	}
	if got := afterStop; got < 5 {
		t.Errorf("heartbeats during a blocked read = %d, want at least 5", got)
	}
	if last.Load() != 3 {
		t.Errorf("heartbeat details = %d, want the 3 tables read", last.Load())
	}
	if got := beats.Load(); got != afterStop {
		t.Errorf("heartbeats after stop: %d, want none", got-afterStop)
	}

	// Outside an activity there is nothing to heartbeat.
	beats.Store(0)
	var read atomic.Int64
	stop := tableHealthHeartbeats(context.Background(), &read)
	time.Sleep(50 * time.Millisecond)
	stop()
	if beats.Load() != 0 {
		t.Errorf("heartbeats outside an activity = %d, want none", beats.Load())
	}
}
