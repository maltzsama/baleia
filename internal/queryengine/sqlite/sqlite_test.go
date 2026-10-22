package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// fixture creates a database file with a table "t" holding one row, inside a
// fresh root.
func fixture(t *testing.T, writable bool) (Options, connections.Config) {
	t.Helper()
	dir := t.TempDir()
	createDatabase(t, filepath.Join(dir, "test.db"), "CREATE TABLE t(a INTEGER)", "INSERT INTO t VALUES (1)")
	cfg := connections.Config{
		Kind:   connections.KindSQLite,
		Values: map[string]string{"path": "test.db"},
	}
	if writable {
		cfg.Values["read_write"] = "true"
	}
	return Options{Root: dir, AllowWrite: writable}, cfg
}

func createDatabase(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func openTestSession(t *testing.T, e queryengine.Engine, spec queryengine.SessionSpec) queryengine.Session {
	t.Helper()
	s, err := e.OpenSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRootConfinement(t *testing.T) {
	dir := t.TempDir()
	createDatabase(t, filepath.Join(dir, "inside.db"), "CREATE TABLE t(a)")
	outside := t.TempDir()
	createDatabase(t, filepath.Join(outside, "outside.db"), "CREATE TABLE t(a)")
	// A symlink inside the root that points outside must be refused.
	if err := os.Symlink(filepath.Join(outside, "outside.db"), filepath.Join(dir, "link.db")); err != nil {
		t.Fatal(err)
	}
	// A relative path that walks out of the root to a real file outside.
	relEscape, err := filepath.Rel(dir, filepath.Join(outside, "outside.db"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		o    Options
		path string
		want queryengine.ErrorCode
	}{
		{"inside", Options{Root: dir}, filepath.Join(dir, "inside.db"), ""},
		{"absolute escape", Options{Root: dir}, filepath.Join(outside, "outside.db"), queryengine.CodePermission},
		{"relative escape", Options{Root: dir}, relEscape, queryengine.CodePermission},
		{"symlink escape", Options{Root: dir}, "link.db", queryengine.CodePermission},
		{"missing", Options{Root: dir}, "nope.db", queryengine.CodeNotFound},
		{"no root", Options{}, "inside.db", queryengine.CodeUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := connections.Config{Kind: connections.KindSQLite, Values: map[string]string{"path": tc.path}}
			e, err := newEngineWithOptions(queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, tc.o)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				_ = e.Close()
				return
			}
			if err == nil {
				_ = e.Close()
				t.Fatalf("want %s, got a working engine", tc.want)
			}
			if code := queryengine.CodeOf(err); code != tc.want {
				t.Fatalf("code = %s, want %s (%v)", code, tc.want, err)
			}
		})
	}
}

func TestRefusesAttachAndTail(t *testing.T) {
	o, cfg := fixture(t, true)
	e, err := newEngineWithOptions(queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s := openTestSession(t, e, queryengine.SessionSpec{})
	defer s.Close()

	for _, stmt := range []string{
		"ATTACH DATABASE 'x.db' AS x",
		"DETACH DATABASE x",
		"VACUUM",
		"VACUUM INTO 'x.db'",
		"SELECT load_extension('evil')",
		"/* hidden */ PRAGMA query_only = 0",
		"-- lead\nATTACH DATABASE 'x.db' AS x",
	} {
		err := s.Execute(context.Background(), stmt, queryengine.Callbacks{}, &discardSink{})
		if code := queryengine.CodeOf(err); code != queryengine.CodePermission {
			t.Errorf("%q: code = %s, want permission (%v)", stmt, code, err)
		}
	}

	err = s.Execute(context.Background(), "SELECT 1; DELETE FROM t", queryengine.Callbacks{}, &discardSink{})
	if code := queryengine.CodeOf(err); code != queryengine.CodeSyntax {
		t.Errorf("tail: code = %s, want syntax (%v)", code, err)
	}
}

func TestReadOnlyEvenWithPragma(t *testing.T) {
	o, cfg := fixture(t, true)
	e, err := newEngineWithOptions(queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s := openTestSession(t, e, queryengine.SessionSpec{ReadOnly: true})
	defer s.Close()

	err = s.Execute(context.Background(), "PRAGMA query_only = 0", queryengine.Callbacks{}, &discardSink{})
	if code := queryengine.CodeOf(err); code != queryengine.CodePermission {
		t.Fatalf("PRAGMA query_only: code = %s, want permission (%v)", code, err)
	}
	err = s.Execute(context.Background(), "INSERT INTO t VALUES (2)", queryengine.Callbacks{}, &discardSink{})
	if code := queryengine.CodeOf(err); code != queryengine.CodePermission {
		t.Fatalf("INSERT: code = %s, want permission (%v)", code, err)
	}
	// The write never happened.
	if got := countRows(t, e); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}

func TestMixedTypesFallback(t *testing.T) {
	dir := t.TempDir()
	createDatabase(t, filepath.Join(dir, "mixed.db"),
		"CREATE TABLE m(x INTEGER)", "INSERT INTO m VALUES (1)", "INSERT INTO m VALUES ('abc')")
	o := Options{Root: dir}
	cfg := connections.Config{Kind: connections.KindSQLite, Values: map[string]string{"path": "mixed.db"}}
	e, err := newEngineWithOptions(queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	tbl, err := queryengine.Collect(context.Background(), e, queryengine.SessionSpec{}, "SELECT x FROM m ORDER BY x", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	if got := tbl.Schema().Field(0).Type; !arrow.TypeEqual(got, arrow.BinaryTypes.String) {
		t.Fatalf("column type = %s, want utf8 (mixed fallback)", got)
	}
	if v, ok := tbl.Schema().Field(0).Metadata.GetValue("baleia.mixed_types"); !ok || v != "true" {
		t.Fatalf("metadata baleia.mixed_types = %q, %v", v, ok)
	}
}

func TestCancelUnsupported(t *testing.T) {
	o, cfg := fixture(t, false)
	e, err := newEngineWithOptions(queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Cancel(context.Background(), queryengine.QueryHandle{EngineID: "t"}); !errors.Is(err, queryengine.ErrCancelUnsupported) {
		t.Fatalf("Cancel = %v, want ErrCancelUnsupported", err)
	}
}

// TestSpec checks the path the composition root uses: the spec's factory
// builds engines with the options it was given.
func TestSpec(t *testing.T) {
	o, cfg := fixture(t, false)
	s := Spec(o)
	if s.Name != Kind {
		t.Fatalf("spec kind = %q, want %q", s.Name, Kind)
	}
	e, err := s.Factory(context.Background(), queryengine.EngineRow{ID: "t", Kind: Kind}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := countRows(t, e); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}

func countRows(t *testing.T, e queryengine.Engine) int64 {
	t.Helper()
	tbl, err := queryengine.Collect(context.Background(), e, queryengine.SessionSpec{}, "SELECT count(*) FROM t", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	col, ok := tbl.Column(0).Data().Chunk(0).(*array.Int64)
	if !ok {
		t.Fatalf("count column is %T, want int64", tbl.Column(0).Data().Chunk(0))
	}
	return col.Value(0)
}

// discardSink ignores every result set.
type discardSink struct{}

func (*discardSink) Begin(*arrow.Schema) error         { return nil }
func (*discardSink) Write(rec arrow.RecordBatch) error { rec.Release(); return nil }
func (*discardSink) End(queryengine.Stats) error       { return nil }
