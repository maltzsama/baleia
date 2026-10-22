package flightsql

import (
	"context"
	"database/sql"
	"net"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql/example"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	"github.com/galileostudio/baleia/internal/queryengine/flightsql/testserver"
)

// TestConformance runs the engine suite against an in-process Flight SQL server
// (the arrow-go SQLite example plus proper gRPC error codes).
func TestConformance(t *testing.T) {
	e := newEngineForTest(t, false)
	conformance.Run(t, func(t *testing.T) queryengine.Engine { return e }, conformance.Fixtures{
		TypesSQL: `SELECT c_int, c_real, c_text, c_blob FROM baleia_conformance_types`,
		Types: []conformance.TypeCase{
			{Column: "c_int", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "c_real", Type: arrow.PrimitiveTypes.Float64, Value: "1.5"},
			{Column: "c_text", Type: arrow.BinaryTypes.String, Value: "ção 漢字 🐳"},
			{Column: "c_blob", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
		},
		ZeroRowsSQL:       "SELECT c_int AS n FROM baleia_conformance_types WHERE 0",
		SetStateSQL:       "SET baleia_state = on",
		ReadStateSQL:      "SHOW baleia_state",
		StateValue:        "on",
		ReadSQL:           "SELECT 1",
		WriteSQL:          "CREATE TABLE baleia_ro (a INTEGER)",
		SleepSQL:          "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 1000000000) SELECT count(*) FROM c",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 5000) SELECT x FROM c",
		CollectCap:        1000,
		IntrospectCatalog: "main",
		IntrospectSchema:  []string{""},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// TestMultiEndpoint reads a statement whose result is split across two
// endpoints, asserting every batch is read.
func TestMultiEndpoint(t *testing.T) {
	e := newEngineForTest(t, true)
	tbl, err := queryengine.Collect(context.Background(), e, queryengine.SessionSpec{},
		"SELECT c_int FROM baleia_conformance_types", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	if tbl.NumRows() != 2 {
		t.Fatalf("rows = %d, want 2 (one per endpoint)", tbl.NumRows())
	}
}

// TestReadOnlyRequiresAssertion: the protocol has no read-only session, so a
// read-only target needs the credentials_read_only assertion.
func TestReadOnlyRequiresAssertion(t *testing.T) {
	e := newEngineForTest(t, false)
	guarded := queryengine.Guard(e, false)
	if _, err := guarded.OpenSession(context.Background(), queryengine.SessionSpec{ReadOnly: true}); queryengine.CodeOf(err) != queryengine.CodeReadOnly {
		t.Fatalf("OpenSession(read-only) = %v, want read_only_unsupported", err)
	}
}

// TestSessionStateIsPerSession: SET/SHOW are tracked per session and do not
// leak into a new one.
func TestSessionStateIsPerSession(t *testing.T) {
	e := newEngineForTest(t, false)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), "SET x = 1", queryengine.Callbacks{}, &discard{}); err != nil {
		t.Fatal(err)
	}
	got, ok := firstValue(t, s, "SHOW x")
	if !ok || got != "1" {
		t.Fatalf("SHOW x = %q, %v", got, ok)
	}
	s.Close()
	fresh, err := e.OpenSession(context.Background(), queryengine.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, ok := firstValue(t, fresh, "SHOW x"); ok {
		t.Fatal("session state leaked into a new session")
	}
}

// --- helpers ---

// newEngineForTest starts an in-process server seeded with the conformance
// fixtures and returns a guarded engine.
func newEngineForTest(t *testing.T, multi bool) queryengine.Engine {
	t.Helper()
	db, err := example.CreateDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seed(t, db)
	addr, stop, err := testserver.Start(db, multi)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := connections.Config{
		Kind:   connections.KindFlightSQL,
		Values: map[string]string{"host": host, "port": port, "tls": "false", "auth": "none"},
	}
	e, err := newEngine(dialect.NewRegistry().MustGet(dialect.FlightSQL))(context.Background(), queryengine.EngineRow{ID: "conformance", Kind: Kind}, cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return queryengine.Guard(e, false)
}

func seed(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE baleia_conformance_types (c_int INTEGER, c_real REAL, c_text TEXT, c_blob BLOB)`,
		`INSERT INTO baleia_conformance_types VALUES (9007199254740993, 1.5, 'ção 漢字 🐳', X'000102ff')`,
		`CREATE TABLE baleia_conformance_table (id INTEGER, name TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func firstValue(t *testing.T, s queryengine.Session, sql string) (string, bool) {
	t.Helper()
	rec := &discard{keep: true}
	if err := s.Execute(context.Background(), sql, queryengine.Callbacks{}, rec); err != nil {
		t.Fatal(err)
	}
	if rec.schema == nil || rec.rec == nil || rec.rec.NumRows() == 0 {
		return "", false
	}
	col := rec.rec.Column(0)
	if col.IsNull(0) {
		return "", false
	}
	return col.ValueStr(0), true
}

// discard is a RecordSink that keeps (or drops) the first batch.
type discard struct {
	keep   bool
	schema *arrow.Schema
	rec    arrow.RecordBatch
}

func (d *discard) Begin(schema *arrow.Schema) error {
	d.schema = schema
	return nil
}

func (d *discard) Write(rec arrow.RecordBatch) error {
	if d.keep && d.rec == nil {
		d.rec = rec
		return nil
	}
	rec.Release()
	return nil
}

func (d *discard) End(queryengine.Stats) error { return nil }
