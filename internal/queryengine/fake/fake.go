// Package fake is a scripted engine for tests. It understands a handful of
// statements, one per conformance case:
//
//	SELECT TYPES        one row of every type the conformance suite checks
//	SELECT ZERO         a result set with a schema and no rows
//	SELECT MULTI        two result sets
//	SET <name> = <v>    session state
//	SHOW <name>         the session value, or no row on a new session
//	BEGIN / COMMIT      a transaction (Close rolls it back)
//	INSERT              adds a row to the session's table
//	SELECT COUNT        rows visible to the session
//	WRITE               a write: permission_denied in a read-only session
//	SLEEP               blocks until cancelled (by context or by handle)
//	ROWS <n>            n rows
//	SELECT * FROM missing  not_found
//
// Anything else is a syntax error. Committed rows and running statements are
// shared by every instance in the process, so a handle can be cancelled from
// another instance, as from another process.
package fake

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Kind is the registered kind of the fake engine.
const Kind = "fake"

func init() {
	queryengine.RegisterKind(Kind, queryengine.ClassDirect, newEngine)
}

// Spec is the kind's entry for a queryengine.Registry (#364).
func Spec() queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine}
}

func newEngine(_ context.Context, row queryengine.EngineRow, _ connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
	return New(Options{ID: row.ID, Dialer: d}), nil
}

// Options configure a fake engine.
type Options struct {
	ID     string
	Dialer *connections.Dialer // recorded so tests can check the factory got it
	// ReadOnly defaults to enforced.
	ReadOnly queryengine.ReadOnlySupport
}

// Engine is the fake engine.
type Engine struct {
	opts   Options
	mu     sync.Mutex
	closed bool
}

// New returns a fake engine.
func New(o Options) *Engine {
	if o.ReadOnly == "" {
		o.ReadOnly = queryengine.ReadOnlyEnforced
	}
	return &Engine{opts: o}
}

// Dialer returns the dialer the engine was built with.
func (e *Engine) Dialer() *connections.Dialer { return e.opts.Dialer }

// Closed reports whether Close was called.
func (e *Engine) Closed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

func (e *Engine) ID() string                   { return e.opts.ID }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.FlightSQL) } // ANSI
func (e *Engine) Ping(context.Context) error   { return nil }

func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:            dialect.FlightSQL,
		IdentityModes:      []string{"connection"},
		ReadOnly:           e.opts.ReadOnly,
		CancelScope:        queryengine.CancelAnyProcess,
		SupportsCancel:     true,
		Transactions:       true,
		MultipleResultSets: true,
		NamespaceDepth:     1,
		SessionSchema:      true,
		IdentifierCase:     "preserve",
	}
}

func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

func (e *Engine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	return &session{engine: e, spec: spec, vars: map[string]string{}}, nil
}

// Cancel stops a running statement of any fake instance.
func (e *Engine) Cancel(_ context.Context, h queryengine.QueryHandle) error {
	running.Lock()
	cancel, ok := running.m[h.QueryID]
	running.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{} }

// Shared state, as an engine server would hold it.
var (
	committed struct {
		sync.Mutex
		rows int64
	}
	running = struct {
		sync.Mutex
		m map[string]context.CancelFunc
	}{m: map[string]context.CancelFunc{}}
	queryCounter struct {
		sync.Mutex
		n int
	}
)

func nextQueryID() string {
	queryCounter.Lock()
	defer queryCounter.Unlock()
	queryCounter.n++
	return fmt.Sprintf("fake_%d", queryCounter.n)
}

type session struct {
	engine  *Engine
	spec    queryengine.SessionSpec
	vars    map[string]string
	inTx    bool
	pending int64
}

func (s *session) Close() error {
	s.inTx, s.pending = false, 0 // rollback
	return nil
}

func errorf(code queryengine.ErrorCode, msg string) error {
	return queryengine.NewError(code, Kind, "", msg)
}

func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	stmt := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	upper := strings.ToUpper(stmt)
	qid := nextQueryID()
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.ID(), QueryID: qid})
	}

	switch {
	case upper == "SELECT TYPES":
		return emit(sink, typesRecord())
	case upper == "SELECT ZERO":
		schema := arrow.NewSchema([]arrow.Field{{Name: "n", Type: arrow.PrimitiveTypes.Int64}}, nil)
		if err := sink.Begin(schema); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{})
	case upper == "SELECT MULTI":
		if err := emit(sink, int64Record("first", 1)); err != nil {
			return err
		}
		return emit(sink, int64Record("second", 2))
	case strings.HasPrefix(upper, "SET "):
		name, value, ok := strings.Cut(stmt[4:], "=")
		if !ok {
			return errorf(queryengine.CodeSyntax, "SET needs name = value")
		}
		s.vars[strings.TrimSpace(strings.ToLower(name))] = strings.Trim(strings.TrimSpace(value), "'")
		return noResult(sink, nil)
	case strings.HasPrefix(upper, "SHOW "):
		name := strings.TrimSpace(strings.ToLower(stmt[5:]))
		return emit(sink, stringRecord(name, s.vars[name], s.vars[name] != ""))
	case upper == "BEGIN":
		s.inTx = true
		return noResult(sink, nil)
	case upper == "COMMIT":
		committed.Lock()
		committed.rows += s.pending
		committed.Unlock()
		s.inTx, s.pending = false, 0
		return noResult(sink, nil)
	case upper == "INSERT":
		if s.spec.ReadOnly {
			return errorf(queryengine.CodePermission, "read-only session")
		}
		if s.inTx {
			s.pending++
		} else {
			committed.Lock()
			committed.rows++
			committed.Unlock()
		}
		one := int64(1)
		return noResult(sink, &one)
	case upper == "SELECT COUNT":
		committed.Lock()
		n := committed.rows
		committed.Unlock()
		return emit(sink, int64Record("count", n+s.pending))
	case upper == "WRITE":
		if s.spec.ReadOnly {
			return errorf(queryengine.CodePermission, "read-only session")
		}
		return noResult(sink, nil)
	case upper == "SLEEP":
		ctx, cancel := context.WithCancel(ctx)
		running.Lock()
		running.m[qid] = cancel
		running.Unlock()
		defer func() {
			running.Lock()
			delete(running.m, qid)
			running.Unlock()
			cancel()
		}()
		<-ctx.Done()
		return errorf(queryengine.CodeCancelled, "query cancelled")
	case strings.HasPrefix(upper, "ROWS "):
		n, err := strconv.ParseInt(strings.TrimSpace(stmt[5:]), 10, 64)
		if err != nil {
			return errorf(queryengine.CodeSyntax, "ROWS needs a number")
		}
		return emitRows(ctx, sink, n)
	case upper == "SELECT * FROM MISSING":
		return errorf(queryengine.CodeNotFound, "table missing does not exist")
	}
	return errorf(queryengine.CodeSyntax, "unknown statement")
}

func noResult(sink queryengine.RecordSink, updated *int64) error {
	if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{UpdateCount: updated})
}

func emit(sink queryengine.RecordSink, rec arrow.RecordBatch) error {
	start := time.Now()
	if err := sink.Begin(rec.Schema()); err != nil {
		rec.Release()
		return err
	}
	rows := rec.NumRows()
	if err := sink.Write(rec); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{RowCount: rows, ElapsedMs: time.Since(start).Milliseconds()})
}

func emitRows(ctx context.Context, sink queryengine.RecordSink, n int64) error {
	schema := arrow.NewSchema([]arrow.Field{{Name: "n", Type: arrow.PrimitiveTypes.Int64}}, nil)
	if err := sink.Begin(schema); err != nil {
		return err
	}
	const batch = 100
	for done := int64(0); done < n; done += batch {
		if err := ctx.Err(); err != nil {
			return errorf(queryengine.CodeCancelled, "query cancelled")
		}
		size := min(batch, n-done)
		b := array.NewInt64Builder(memory.DefaultAllocator)
		for i := int64(0); i < size; i++ {
			b.Append(done + i)
		}
		col := b.NewArray()
		b.Release()
		rec := array.NewRecordBatch(schema, []arrow.Array{col}, size)
		col.Release()
		if err := sink.Write(rec); err != nil {
			return err
		}
	}
	return sink.End(queryengine.Stats{RowCount: n})
}

func int64Record(name string, v int64) arrow.RecordBatch {
	schema := arrow.NewSchema([]arrow.Field{{Name: name, Type: arrow.PrimitiveTypes.Int64}}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.Int64Builder).Append(v)
	return b.NewRecordBatch()
}

func stringRecord(name, v string, present bool) arrow.RecordBatch {
	schema := arrow.NewSchema([]arrow.Field{{Name: name, Type: arrow.BinaryTypes.String, Nullable: true}}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	if present {
		b.Field(0).(*array.StringBuilder).Append(v)
	}
	return b.NewRecordBatch()
}

// TypesValues are the values typesRecord emits, as Array.ValueStr renders them.
var TypesValues = map[string]string{
	"bool":        "true",
	"int64":       "9007199254740993",
	"decimal":     "1234567890123456789012345678.0123456789",
	"date":        "2024-02-29",
	"timestamp":   "2024-02-29T13:45:30.123456",
	"timestamptz": "2024-02-29T13:45:30.123456Z",
	"varchar":     "ação 漢字 🐳",
	"null":        "(null)",
	"binary":      "AAEC/w==",
}

func typesRecord() arrow.RecordBatch {
	dec := &arrow.Decimal128Type{Precision: 38, Scale: 10}
	tsUTC := &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}
	ts := &arrow.TimestampType{Unit: arrow.Microsecond}
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "bool", Type: arrow.FixedWidthTypes.Boolean, Nullable: true},
		{Name: "int64", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
		{Name: "decimal", Type: dec, Nullable: true},
		{Name: "date", Type: arrow.FixedWidthTypes.Date32, Nullable: true},
		{Name: "timestamp", Type: ts, Nullable: true},
		{Name: "timestamptz", Type: tsUTC, Nullable: true},
		{Name: "varchar", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "null", Type: arrow.Null, Nullable: true},
		{Name: "binary", Type: arrow.BinaryTypes.Binary, Nullable: true},
	}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()

	b.Field(0).(*array.BooleanBuilder).Append(true)
	b.Field(1).(*array.Int64Builder).Append(9007199254740993)
	d, err := decimal128.FromString("1234567890123456789012345678.0123456789", 38, 10)
	if err != nil {
		panic(err)
	}
	b.Field(2).(*array.Decimal128Builder).Append(d)
	day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	b.Field(3).(*array.Date32Builder).Append(arrow.Date32FromTime(day))
	moment := time.Date(2024, 2, 29, 13, 45, 30, 123456000, time.UTC)
	b.Field(4).(*array.TimestampBuilder).Append(arrow.Timestamp(moment.UnixMicro()))
	b.Field(5).(*array.TimestampBuilder).Append(arrow.Timestamp(moment.UnixMicro()))
	b.Field(6).(*array.StringBuilder).Append("ação 漢字 🐳")
	b.Field(7).(*array.NullBuilder).AppendNull()
	b.Field(8).(*array.BinaryBuilder).Append([]byte{0, 1, 2, 255})
	return b.NewRecordBatch()
}

type introspector struct{}

func (introspector) ListCatalogs(context.Context) ([]string, error) { return []string{"fake"}, nil }

func (introspector) ListSchemas(_ context.Context, catalog string) ([][]string, error) {
	if catalog != "fake" {
		return nil, errorf(queryengine.CodeNotFound, "catalog not found")
	}
	return [][]string{{"main"}}, nil
}

func (introspector) ListTables(_ context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	if catalog != "fake" || len(schema) != 1 || schema[0] != "main" {
		return nil, errorf(queryengine.CodeNotFound, "schema not found")
	}
	return []connections.TableRef{{Catalog: "fake", Namespace: connections.Namespace{"main"}, Name: "events", Type: "TABLE"}}, nil
}

func (introspector) DescribeTable(_ context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if t.Name != "events" {
		return nil, errorf(queryengine.CodeNotFound, "table not found")
	}
	return &connections.TableMeta{Ref: t, Columns: []connections.Column{
		{Name: "id", NativeType: "bigint", Position: 1},
		{Name: "payload", NativeType: "varchar", Nullable: true, Position: 2},
	}}, nil
}
