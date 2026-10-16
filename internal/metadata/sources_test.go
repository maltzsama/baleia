package metadata

import (
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// TestSourceFor: catalog managers and walking databases are read from their
// own source; Trino connections (adopted catalogs), Flight SQL and kinds with
// no direct metadata role (Hive Metastore, #86) go through an engine.
func TestSourceFor(t *testing.T) {
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	want := map[connections.Kind]string{
		connections.KindPolaris:       PathProvider,
		connections.KindNessie:        PathProvider,
		connections.KindIcebergREST:   PathProvider,
		connections.KindPostgreSQL:    PathProvider,
		connections.KindMySQL:         PathProvider,
		connections.KindMariaDB:       PathProvider,
		connections.KindSQLServer:     PathProvider,
		connections.KindOracle:        PathProvider,
		connections.KindSQLite:        PathProvider,
		connections.KindClickHouse:    PathProvider,
		connections.KindTrino:         PathEngine,
		connections.KindFlightSQL:     PathEngine,
		connections.KindHiveMetastore: PathEngine,
		connections.KindGlue:          PathProvider,
	}
	for kind, path := range want {
		p, _ := reg.Get(kind)
		if got := PathFor(p); got != path {
			t.Errorf("%s: path %s, want %s", kind, got, path)
		}
		if path == PathProvider && p != nil && p.Family() != connections.FamilyIceberg && typeSystems[kind] == "" {
			t.Errorf("%s: a walking provider needs a type system", kind)
		}
	}
}

// fakeReader is an Iceberg REST-like reader with two namespace levels; it
// counts full loads and answers a matching ETag with "unchanged".
type fakeReader struct {
	connections.MetadataReader
	loads, walks int
	etag         string
}

func (r *fakeReader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: "def"}}, nil
}

func (r *fakeReader) ListNamespaces(_ context.Context, _ string, parent connections.Namespace) ([]connections.Namespace, error) {
	switch len(parent) {
	case 0:
		return []connections.Namespace{{"db"}}, nil
	case 1:
		return []connections.Namespace{{"db", "inner"}}, nil
	}
	return nil, nil
}

func (r *fakeReader) ListTables(_ context.Context, catalog string, ns connections.Namespace, token string) ([]connections.TableRef, string, error) {
	if len(ns) != 2 {
		return nil, "", nil
	}
	if token == "" {
		return []connections.TableRef{{Catalog: catalog, Namespace: ns, Name: "a"}}, "p2", nil
	}
	return []connections.TableRef{{Catalog: catalog, Namespace: ns, Name: "b"}}, "", nil
}

func (r *fakeReader) LoadTableIfChanged(_ context.Context, t connections.TableRef, etag string) (*connections.TableMeta, string, error) {
	if etag != "" && etag == r.etag {
		return nil, etag, nil
	}
	r.loads++
	return &connections.TableMeta{
		Ref:        t,
		Columns:    []connections.Column{{Name: "id", NativeType: "long", Position: 1}},
		Properties: map[string]string{"metadata_location": "s3://m/" + t.Name + "/" + r.etag},
	}, r.etag, nil
}

func (r *fakeReader) WalkCatalog(_ context.Context, catalog string, visit func(connections.TableMeta) error) error {
	r.walks++
	return visit(connections.TableMeta{Ref: connections.TableRef{Catalog: catalog, Namespace: connections.Namespace{"public"}, Name: "orders", Type: "TABLE"},
		Columns: []connections.Column{{Name: "id", NativeType: "bigint", Position: 1}}})
}

func (r *fakeReader) LoadTable(context.Context, connections.TableRef) (*connections.TableMeta, error) {
	r.loads++
	return nil, connections.ErrNotFound
}

func (r *fakeReader) Close() error { return nil }

type recorder struct {
	namespaces [][]string
	tables     []TableSnapshot
}

func (r *recorder) Namespace(ns []string) error { r.namespaces = append(r.namespaces, ns); return nil }
func (r *recorder) Table(t TableSnapshot) error { r.tables = append(r.tables, t); return nil }

// TestIcebergSource_ConditionalLoads: nested namespaces and table pages are
// walked; a table whose stored ETag still matches is reported unchanged
// without a load, and the fingerprint is the metadata-location.
func TestIcebergSource_ConditionalLoads(t *testing.T) {
	r := &fakeReader{etag: `W/"1"`}
	src := &icebergSource{r: r, catalog: "wh"}
	none := func([]string, string) (Known, bool) { return Known{}, false }
	rec := &recorder{}
	if err := src.Walk(context.Background(), none, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.namespaces) != 2 || len(rec.tables) != 2 || r.loads != 2 {
		t.Fatalf("namespaces %v tables %d loads %d", rec.namespaces, len(rec.tables), r.loads)
	}
	first := rec.tables[0]
	if first.Fingerprint != `s3://m/a/W/"1"` || first.Properties["etag"] != `W/"1"` || first.Unchanged || len(first.Columns) != 1 {
		t.Fatalf("first table = %+v", first)
	}

	r.loads = 0
	stored := func(ns []string, name string) (Known, bool) {
		return Known{Fingerprint: "s3://m/" + name + `/W/"1"`, ETag: `W/"1"`}, true
	}
	rec = &recorder{}
	if err := src.Walk(context.Background(), stored, rec); err != nil {
		t.Fatal(err)
	}
	if r.loads != 0 || !rec.tables[0].Unchanged || !rec.tables[1].Unchanged {
		t.Fatalf("unchanged re-walk: loads %d tables %+v", r.loads, rec.tables)
	}
}

// TestProviderSource_DatabaseSingleQuery: a database catalog is read with
// one walk query, never a load per table; an unchanged table is reported
// unchanged.
func TestProviderSource_DatabaseSingleQuery(t *testing.T) {
	r := &fakeReader{}
	src := &walkerSource{r: r, w: r, catalog: "sales", typeSystem: "postgresql"}
	rec := &recorder{}
	none := func([]string, string) (Known, bool) { return Known{}, false }
	if err := src.Walk(context.Background(), none, rec); err != nil {
		t.Fatal(err)
	}
	if r.walks != 1 || r.loads != 0 || len(rec.tables) != 1 || rec.tables[0].Unchanged {
		t.Fatalf("walks %d loads %d tables %+v", r.walks, r.loads, rec.tables)
	}
	fp := rec.tables[0].Fingerprint
	rec = &recorder{}
	same := func([]string, string) (Known, bool) { return Known{Fingerprint: fp}, true }
	if err := src.Walk(context.Background(), same, rec); err != nil {
		t.Fatal(err)
	}
	if !rec.tables[0].Unchanged {
		t.Fatal("same fingerprint: unchanged")
	}
}

// fakeIntrospectEngine answers the dialect's schemas and catalog queries and
// records the session spec.
type fakeIntrospectEngine struct {
	queryengine.Engine
	spec queryengine.SessionSpec
	sqls []string
}

func (e *fakeIntrospectEngine) Kind() string { return "trino" }
func (e *fakeIntrospectEngine) Dialect() queryengine.Dialect {
	return dialect.MustGet(dialect.Trino)
}
func (e *fakeIntrospectEngine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	e.spec = spec
	return &fakeIntrospectSession{e: e}, nil
}

type fakeIntrospectSession struct{ e *fakeIntrospectEngine }

func (s *fakeIntrospectSession) Close() error { return nil }

func (s *fakeIntrospectSession) Execute(_ context.Context, sql string, _ queryengine.Callbacks, sink queryengine.RecordSink) error {
	s.e.sqls = append(s.e.sqls, sql)
	rows := [][]string{{"sales"}, {"empty"}}
	if sql == dialect.MustGet(dialect.Trino).MetadataQueries().Catalog {
		rows = [][]string{
			{"sales", "orders", "TABLE", "id", "bigint", "NO", "1"},
			{"sales", "orders", "TABLE", "note", "varchar", "YES", "2"},
			{"sales", "v", "VIEW", "id", "bigint", "YES", "1"},
		}
	}
	return emitStrings(sink, rows)
}

func emitStrings(sink queryengine.RecordSink, rows [][]string) error {
	n := len(rows[0])
	fields := make([]arrow.Field, n)
	for i := range fields {
		fields[i] = arrow.Field{Name: string(rune('a' + i)), Type: arrow.BinaryTypes.String, Nullable: true}
	}
	schema := arrow.NewSchema(fields, nil)
	b := array.NewRecordBuilder(memory.NewGoAllocator(), schema)
	defer b.Release()
	for _, r := range rows {
		for i, v := range r {
			b.Field(i).(*array.StringBuilder).Append(v)
		}
	}
	rec := b.NewRecordBatch()
	if err := sink.Begin(schema); err != nil {
		return err
	}
	if err := sink.Write(rec); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{RowCount: int64(len(rows))})
}

// TestEngineSource_ReadOnlySession: introspection runs in a read-only
// session with the engine's service identity, in the binding's catalog, with
// two queries per catalog (schemas, then every column): never one per table.
func TestEngineSource_ReadOnlySession(t *testing.T) {
	eng := &fakeIntrospectEngine{}
	src := &engineSource{eng: eng, catalog: "lake", queries: eng.Dialect().MetadataQueries(), typeSystem: "trino"}
	rec := &recorder{}
	if err := src.Walk(context.Background(), func([]string, string) (Known, bool) { return Known{}, false }, rec); err != nil {
		t.Fatal(err)
	}
	if !eng.spec.ReadOnly || eng.spec.Identity != (queryengine.Identity{}) || eng.spec.Catalog != "lake" || eng.spec.Tags.Source != "sync" {
		t.Fatalf("session spec = %+v", eng.spec)
	}
	if len(eng.sqls) != 2 {
		t.Fatalf("queries = %d, want 2 (schemas, catalog)", len(eng.sqls))
	}
	if len(rec.namespaces) != 2 || len(rec.tables) != 2 {
		t.Fatalf("namespaces %v tables %+v", rec.namespaces, rec.tables)
	}
	orders := rec.tables[0]
	if orders.Kind != KindTable || len(orders.Columns) != 2 || orders.Columns[0].Nullable || !orders.Columns[1].Nullable || orders.Columns[1].Ordinal != 2 {
		t.Fatalf("orders = %+v", orders)
	}
	if rec.tables[1].Kind != KindView {
		t.Fatalf("view kind = %q", rec.tables[1].Kind)
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	got := truncate("aé", 2) // "é" is 2 bytes: cutting at 2 splits it
	if got != "a" {
		t.Fatalf("truncate = %q", got)
	}
}
