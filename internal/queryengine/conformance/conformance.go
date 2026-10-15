// Package conformance is the suite every engine adapter must pass unchanged
// (#20): fake now, Trino and the direct engines as they land.
package conformance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// TypeCase is one column of Fixtures.TypesSQL.
type TypeCase struct {
	Column string
	Type   arrow.DataType
	// Value is the first row's value as arrow's Array.ValueStr renders it.
	Value string
	// Skip marks a type the engine does not have.
	Skip bool
}

// Fixtures are the engine's spellings of each case. Optional cases run only
// when their fields are set (and the capability allows).
type Fixtures struct {
	TypesSQL string
	Types    []TypeCase

	ZeroRowsSQL    string
	MultiResultSQL string // optional: a statement with two result sets

	// SetStateSQL sets session state; ReadStateSQL reads it back as one string
	// value equal to StateValue, and returns no row (or a null) on a new
	// session.
	SetStateSQL, ReadStateSQL, StateValue string

	// Transaction case (when Capabilities.Transactions): Begin, Insert, then
	// Count returns one more row in the session; a new session after Close
	// sees the old count.
	BeginSQL, InsertSQL, CountSQL string

	ReadSQL  string // allowed in a read-only session
	WriteSQL string // refused in a read-only session

	SleepSQL string // runs until cancelled

	SyntaxErrorSQL  string
	MissingTableSQL string

	ManyRowsSQL string // returns more than CollectCap rows
	CollectCap  int64

	// Introspection (when the engine has an Introspector): this table and
	// columns must be listed.
	IntrospectCatalog string
	IntrospectSchema  []string
	IntrospectTable   string
	IntrospectColumns []string
}

// Run runs the suite. newEngine returns a fresh instance each call; the
// cross-process cancel case uses two. Engines are tested as the backend sees
// them: behind queryengine.Guard, with no read-only credentials assertion.
func Run(t *testing.T, newEngine func(t *testing.T) queryengine.Engine, fx Fixtures) {
	t.Helper()
	ctx := context.Background()
	raw := newEngine
	newEngine = func(t *testing.T) queryengine.Engine { return queryengine.Guard(raw(t), false) }
	e := newEngine(t)
	caps := e.Capabilities()

	t.Run("capabilities", func(t *testing.T) {
		if caps.Dialect == "" || caps.Dialect != e.Dialect().Name() {
			t.Errorf("capabilities name dialect %q, Dialect() is %q", caps.Dialect, e.Dialect().Name())
		}
		if caps.SupportsCancel != (caps.CancelScope != "") {
			t.Error("SupportsCancel and CancelScope disagree")
		}
		switch caps.IdentifierCase {
		case "preserve", "lower", "upper":
		default:
			t.Errorf("identifier case %q", caps.IdentifierCase)
		}
		if caps.NamespaceDepth < 0 {
			t.Errorf("namespace depth %d", caps.NamespaceDepth)
		}
	})

	t.Run("types", func(t *testing.T) {
		tbl, err := queryengine.Collect(ctx, e, queryengine.SessionSpec{}, fx.TypesSQL, 10)
		if err != nil {
			t.Fatal(err)
		}
		defer tbl.Release()
		for _, tc := range fx.Types {
			if tc.Skip {
				continue
			}
			idx := tbl.Schema().FieldIndices(tc.Column)
			if len(idx) != 1 {
				t.Errorf("%s: column missing", tc.Column)
				continue
			}
			col := tbl.Column(idx[0])
			if !arrow.TypeEqual(col.DataType(), tc.Type) {
				t.Errorf("%s: type %s, want %s", tc.Column, col.DataType(), tc.Type)
				continue
			}
			if col.Len() < 1 {
				t.Errorf("%s: no value", tc.Column)
				continue
			}
			if got := col.Data().Chunk(0).ValueStr(0); got != tc.Value {
				t.Errorf("%s: value %q, want %q", tc.Column, got, tc.Value)
			}
		}
	})

	t.Run("zero rows", func(t *testing.T) {
		rec := &recorder{}
		runOne(t, e, queryengine.SessionSpec{}, fx.ZeroRowsSQL, rec)
		if len(rec.sets) != 1 || rec.sets[0].schema == nil || rec.sets[0].schema.NumFields() == 0 || rec.sets[0].batches != 0 || !rec.sets[0].ended {
			t.Fatalf("result sets = %+v, want one with a schema, no batches, ended", rec.sets)
		}
	})

	t.Run("multiple result sets", func(t *testing.T) {
		if fx.MultiResultSQL == "" || !caps.MultipleResultSets {
			t.Skip("engine has no multi-result statements")
		}
		rec := &recorder{}
		runOne(t, e, queryengine.SessionSpec{}, fx.MultiResultSQL, rec)
		if len(rec.sets) != 2 || !rec.sets[0].ended || !rec.sets[1].ended {
			t.Fatalf("result sets = %+v, want two, in order", rec.sets)
		}
	})

	t.Run("session state", func(t *testing.T) {
		s := open(t, e, queryengine.SessionSpec{})
		exec(t, s, fx.SetStateSQL, nil)
		if got := firstString(t, s, fx.ReadStateSQL); got != fx.StateValue {
			t.Fatalf("state on the same session = %q, want %q", got, fx.StateValue)
		}
		s.Close()
		fresh := open(t, e, queryengine.SessionSpec{})
		defer fresh.Close()
		if got := firstString(t, fresh, fx.ReadStateSQL); got == fx.StateValue {
			t.Fatal("session state leaked into a new session")
		}
	})

	t.Run("transaction", func(t *testing.T) {
		if !caps.Transactions || fx.BeginSQL == "" {
			t.Skip("engine has no transactions")
		}
		before := firstInt(t, e, fx.CountSQL)
		s := open(t, e, queryengine.SessionSpec{})
		exec(t, s, fx.BeginSQL, nil)
		exec(t, s, fx.InsertSQL, nil)
		rec := &recorder{}
		exec(t, s, fx.CountSQL, rec)
		if got := rec.firstInt(t); got != before+1 {
			t.Fatalf("count inside the transaction = %d, want %d", got, before+1)
		}
		s.Close()
		if got := firstInt(t, e, fx.CountSQL); got != before {
			t.Fatalf("count after Close = %d, want %d (rolled back)", got, before)
		}
	})

	t.Run("read-only", func(t *testing.T) {
		spec := queryengine.SessionSpec{ReadOnly: true}
		s, err := e.OpenSession(ctx, spec)
		if caps.ReadOnly == queryengine.ReadOnlyNone {
			if queryengine.CodeOf(err) != queryengine.CodeReadOnly {
				t.Fatalf("OpenSession(read-only) on an engine without enforcement: err = %v, want read_only_unsupported", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		exec(t, s, fx.ReadSQL, nil)
		err = s.Execute(ctx, fx.WriteSQL, queryengine.Callbacks{}, &recorder{})
		if queryengine.CodeOf(err) != queryengine.CodePermission {
			t.Fatalf("write in a read-only session: err = %v, want permission_denied", err)
		}
	})

	t.Run("cancel in process", func(t *testing.T) {
		s := open(t, e, queryengine.SessionSpec{})
		defer s.Close()
		cctx, cancel := context.WithCancel(ctx)
		started := make(chan struct{})
		var once sync.Once
		done := make(chan error, 1)
		go func() {
			done <- s.Execute(cctx, fx.SleepSQL, queryengine.Callbacks{OnStart: func(queryengine.QueryHandle) { once.Do(func() { close(started) }) }}, &recorder{})
		}()
		waitStarted(t, started)
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a cancelled statement returned no error")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("statement still running 10 s after cancel")
		}
	})

	t.Run("cancel cross process", func(t *testing.T) {
		if caps.CancelScope != queryengine.CancelAnyProcess {
			if err := e.Cancel(ctx, queryengine.QueryHandle{EngineID: e.ID(), QueryID: "x"}); !errors.Is(err, queryengine.ErrCancelUnsupported) {
				t.Fatalf("Cancel on an in-process engine: err = %v, want ErrCancelUnsupported", err)
			}
			return
		}
		s := open(t, e, queryengine.SessionSpec{})
		defer s.Close()
		handles := make(chan queryengine.QueryHandle, 1)
		done := make(chan error, 1)
		go func() {
			done <- s.Execute(ctx, fx.SleepSQL, queryengine.Callbacks{OnStart: func(h queryengine.QueryHandle) { handles <- h }}, &recorder{})
		}()
		var h queryengine.QueryHandle
		select {
		case h = <-handles:
		case <-time.After(10 * time.Second):
			t.Fatal("OnStart never reported a handle")
		}
		other := newEngine(t) // another process
		if err := other.Cancel(ctx, h); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if queryengine.CodeOf(err) != queryengine.CodeCancelled {
				t.Fatalf("err = %v, want cancelled", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("statement still running 10 s after Cancel(handle)")
		}
		// Cancelling a statement that is gone is not an error.
		if err := other.Cancel(ctx, h); err != nil {
			t.Fatalf("second cancel: %v", err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for sql, want := range map[string]queryengine.ErrorCode{fx.SyntaxErrorSQL: queryengine.CodeSyntax, fx.MissingTableSQL: queryengine.CodeNotFound} {
			s := open(t, e, queryengine.SessionSpec{})
			err := s.Execute(ctx, sql, queryengine.Callbacks{}, &recorder{})
			s.Close()
			if got := queryengine.CodeOf(err); got != want {
				t.Errorf("%q: code %q (%v), want %q", sql, got, err, want)
			}
		}
	})

	t.Run("introspection", func(t *testing.T) {
		in := e.Introspector()
		if in == nil {
			t.Skip("engine has no introspector")
		}
		cats, err := in.ListCatalogs(ctx)
		if err != nil || !slices.Contains(cats, fx.IntrospectCatalog) {
			t.Fatalf("catalogs = %v, %v", cats, err)
		}
		schemas, err := in.ListSchemas(ctx, fx.IntrospectCatalog)
		if err != nil || !slices.ContainsFunc(schemas, func(s []string) bool { return slices.Equal(s, fx.IntrospectSchema) }) {
			t.Fatalf("schemas = %v, %v", schemas, err)
		}
		tables, err := in.ListTables(ctx, fx.IntrospectCatalog, fx.IntrospectSchema)
		i := slices.IndexFunc(tables, func(r connections.TableRef) bool { return r.Name == fx.IntrospectTable })
		if err != nil || i < 0 {
			t.Fatalf("tables = %v, %v", tables, err)
		}
		meta, err := in.DescribeTable(ctx, tables[i])
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for _, c := range meta.Columns {
			cols = append(cols, c.Name)
		}
		if !slices.Equal(cols, fx.IntrospectColumns) {
			t.Fatalf("columns = %v, want %v", cols, fx.IntrospectColumns)
		}
	})

	t.Run("collect cap", func(t *testing.T) {
		_, err := queryengine.Collect(ctx, e, queryengine.SessionSpec{}, fx.ManyRowsSQL, fx.CollectCap)
		if queryengine.CodeOf(err) != queryengine.CodeResource {
			t.Fatalf("err = %v, want resource_exhausted", err)
		}
	})
}

type resultSet struct {
	schema  *arrow.Schema
	batches int
	rows    int64
	first   arrow.RecordBatch
	ended   bool
}

// recorder is a RecordSink that keeps the first batch of each result set.
type recorder struct{ sets []*resultSet }

func (r *recorder) Begin(schema *arrow.Schema) error {
	r.sets = append(r.sets, &resultSet{schema: schema})
	return nil
}

func (r *recorder) Write(rec arrow.RecordBatch) error {
	cur := r.sets[len(r.sets)-1]
	cur.batches++
	cur.rows += rec.NumRows()
	if cur.first == nil {
		cur.first = rec
		return nil
	}
	rec.Release()
	return nil
}

func (r *recorder) End(queryengine.Stats) error {
	r.sets[len(r.sets)-1].ended = true
	return nil
}

func (r *recorder) firstValue(t *testing.T) (string, bool) {
	t.Helper()
	if len(r.sets) == 0 || r.sets[0].first == nil || r.sets[0].first.NumRows() == 0 {
		return "", false
	}
	col := r.sets[0].first.Column(0)
	if col.IsNull(0) {
		return "", false
	}
	return col.ValueStr(0), true
}

func (r *recorder) firstInt(t *testing.T) int64 {
	t.Helper()
	v, ok := r.firstValue(t)
	if !ok {
		t.Fatal("no value")
	}
	var n int64
	for _, c := range strings.TrimSpace(v) {
		if c < '0' || c > '9' {
			t.Fatalf("not an integer: %q", v)
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func open(t *testing.T, e queryengine.Engine, spec queryengine.SessionSpec) queryengine.Session {
	t.Helper()
	s, err := e.OpenSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func exec(t *testing.T, s queryengine.Session, sql string, rec *recorder) {
	t.Helper()
	if rec == nil {
		rec = &recorder{}
	}
	if err := s.Execute(context.Background(), sql, queryengine.Callbacks{}, rec); err != nil {
		t.Fatalf("%q: %v", sql, err)
	}
}

func runOne(t *testing.T, e queryengine.Engine, spec queryengine.SessionSpec, sql string, rec *recorder) {
	t.Helper()
	s := open(t, e, spec)
	defer s.Close()
	exec(t, s, sql, rec)
}

func firstString(t *testing.T, s queryengine.Session, sql string) string {
	t.Helper()
	rec := &recorder{}
	exec(t, s, sql, rec)
	v, _ := rec.firstValue(t)
	return v
}

func firstInt(t *testing.T, e queryengine.Engine, sql string) int64 {
	t.Helper()
	rec := &recorder{}
	runOne(t, e, queryengine.SessionSpec{}, sql, rec)
	return rec.firstInt(t)
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("statement never started")
	}
}
