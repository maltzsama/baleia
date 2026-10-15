package activities

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/jackc/pgx/v5"
)

// fakeQuerier embeds db.Querier so only the exercised method is implemented.
type fakeQuerier struct {
	db.Querier
	updateErr error
}

func (f *fakeQuerier) UpdateQueryExecution(context.Context, db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	return db.QueryExecution{}, f.updateErr
}

// fakeEngine is a scripted engine (and engine source) so the activities can
// be tested against a blocked, failing, or always-succeeding backend. The
// embedded Trino adapter is nil: its dialect, capabilities and maintenance
// rendering need no coordinator, so the SQL under test is Trino's own.
type fakeEngine struct {
	*qtrino.Engine
	mu        sync.Mutex
	callCount int
	block     time.Duration
	err       error
	assignID  bool
	deliver   int
	noColumns bool
	killCalls int
	lastUser  string
	ctasCount string // when set, the result is one row holding this count
	sqls      []string
	lastSpec  queryengine.SessionSpec
	gotEngine []string // engine ids asked for through Get ("default" for DefaultTrino)
	failOn    string   // this statement fails with a syntax error
	sessions  int      // sessions opened
	closed    int      // sessions closed
	cancelled []queryengine.QueryHandle
	started   chan struct{} // closed when a blocking statement starts
	// beginThenBlock streams one row, then blocks like a slow engine.
	beginThenBlock bool
}

func (f *fakeEngine) DefaultTrino(context.Context) (queryengine.Engine, error) {
	f.mu.Lock()
	f.gotEngine = append(f.gotEngine, "default")
	f.mu.Unlock()
	return f, nil
}

func (f *fakeEngine) Get(_ context.Context, id pgtype.UUID) (queryengine.Engine, error) {
	f.mu.Lock()
	f.gotEngine = append(f.gotEngine, uuid.UUID(id.Bytes).String())
	f.mu.Unlock()
	return f, nil
}

func (f *fakeEngine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	f.mu.Lock()
	f.sessions++
	f.mu.Unlock()
	return &fakeSession{f: f, spec: spec, vars: map[string]string{}}, nil
}

func (f *fakeEngine) Cancel(_ context.Context, h queryengine.QueryHandle) error {
	f.mu.Lock()
	f.killCalls++
	f.cancelled = append(f.cancelled, h)
	f.mu.Unlock()
	return nil
}

// fakeSession keeps SET variables for its lifetime, like a database
// connection: "SET x=1" then "SELECT x" returns 1 on the same session only.
type fakeSession struct {
	f    *fakeEngine
	spec queryengine.SessionSpec
	vars map[string]string
}

func (s *fakeSession) Close() error {
	s.f.mu.Lock()
	s.f.closed++
	s.f.mu.Unlock()
	return nil
}

func (s *fakeSession) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	f := s.f
	f.mu.Lock()
	f.callCount++
	f.lastUser = s.spec.Identity.UserID
	f.lastSpec = s.spec
	f.sqls = append(f.sqls, sql)
	f.mu.Unlock()

	if f.ctasCount != "" {
		return emitBigints(sink, []string{f.ctasCount})
	}
	if f.failOn != "" && sql == f.failOn {
		return queryengine.NewError(queryengine.CodeSyntax, "fake", "", "syntax error")
	}
	if k, v, ok := strings.Cut(strings.TrimPrefix(sql, "SET "), "="); ok && strings.HasPrefix(sql, "SET ") {
		s.vars[k] = v
		if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{})
	}
	if k, ok := strings.CutPrefix(sql, "SELECT var "); ok {
		v, set := s.vars[k]
		if !set {
			return queryengine.NewError(queryengine.CodeNotFound, "fake", "", "unknown variable "+k)
		}
		return emitBigints(sink, []string{v})
	}
	if f.assignID && cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{QueryID: testQueryID})
	}
	if f.err != nil {
		return f.err
	}
	if f.beginThenBlock {
		b, err := arrowipc.NewBatcher([]arrowipc.ColumnInfo{{Name: "c", Type: "bigint"}})
		if err != nil {
			return err
		}
		defer b.Release()
		if err := sink.Begin(b.Schema()); err != nil {
			return err
		}
		if err := b.Append([][]interface{}{{json.Number("1")}}, sink.Write); err != nil {
			return err
		}
		if err := b.Flush(sink.Write); err != nil {
			return err
		}
	}
	if f.block > 0 {
		if f.started != nil {
			close(f.started)
		}
		select {
		case <-time.After(f.block):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.noColumns {
		// DDL/DML: one result set without columns.
		if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{})
	}
	n := f.deliver
	if n == 0 {
		n = 1
	}
	vals := make([]string, n)
	for i := range vals {
		vals[i] = "1"
	}
	if err := emitBigints(sink, vals); err != nil {
		return err
	}
	if cb.OnProgress != nil {
		cb.OnProgress(queryengine.Progress{})
	}
	return nil
}

// emitBigints streams one bigint column "c" in batches of one row, as an
// engine page at a time.
func emitBigints(sink queryengine.RecordSink, vals []string) error {
	b, err := arrowipc.NewBatcher([]arrowipc.ColumnInfo{{Name: "c", Type: "bigint"}})
	if err != nil {
		return err
	}
	defer b.Release()
	if err := sink.Begin(b.Schema()); err != nil {
		return err
	}
	for _, v := range vals {
		if err := b.Append([][]interface{}{{json.Number(v)}}, sink.Write); err != nil {
			return err
		}
		if err := b.Flush(sink.Write); err != nil {
			return err
		}
	}
	return sink.End(queryengine.Stats{RowCount: int64(len(vals))})
}

// handleQuerier records the query handles the activity persists.
type handleQuerier struct {
	db.Querier
	mu      sync.Mutex
	handles []db.SetStatementQueryHandleParams
}

func (q *handleQuerier) SetStatementQueryHandle(_ context.Context, p db.SetStatementQueryHandleParams) error {
	q.mu.Lock()
	q.handles = append(q.handles, p)
	q.mu.Unlock()
	return nil
}

// fakeS3 drains the upload stream and reports a fixed size or error.
type fakeS3 struct {
	mu          sync.Mutex
	bytes       int64
	err         error
	uploadCalls int
	readErrs    []error // what reading each upload stream ended with
}

func (f *fakeS3) PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error) {
	_, rerr := io.Copy(io.Discard, r)
	f.mu.Lock()
	f.uploadCalls++
	f.readErrs = append(f.readErrs, rerr)
	f.mu.Unlock()
	if rerr != nil {
		return 0, rerr
	}
	if f.err != nil {
		return 0, f.err
	}
	return f.bytes, nil
}

func testActivitiesConfig() *config.Config {
	return &config.Config{ResultMaxRows: 1000000, ResultMaxBytes: 536870912}
}

const (
	testExecID  = "00000000-0000-0000-0000-000000000001"
	testQueryID = "20240101_000000_00000_abcde"
)

// TestCompleteExecutionDoesNotOverwriteTerminal pins the race fix: when the
// conditional update matches nothing (the execution was cancelled), the activity
// must succeed without overwriting the terminal status.
func TestCompleteExecutionDoesNotOverwriteTerminal(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: pgx.ErrNoRows}}

	err := a.completeExecution(context.Background(), CompleteExecutionParams{
		ExecutionID: testExecID,
		Status:      "finished",
	})
	if err != nil {
		t.Fatalf("completeExecution must not fail when the execution is already terminal: %v", err)
	}
}

func TestFailExecutionDoesNotOverwriteTerminal(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: pgx.ErrNoRows}}

	err := a.failExecution(context.Background(), FailExecutionParams{
		ExecutionID: testExecID,
	})
	if err != nil {
		t.Fatalf("failExecution must not fail when the execution is already terminal: %v", err)
	}
}

func TestCompleteExecutionPropagatesRealErrors(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: context.DeadlineExceeded}}

	err := a.completeExecution(context.Background(), CompleteExecutionParams{
		ExecutionID: testExecID,
		Status:      "finished",
	})
	if err == nil {
		t.Fatal("completeExecution must propagate non-ErrNoRows errors")
	}
}
