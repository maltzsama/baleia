package activities

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/jackc/pgx/v5"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
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
}

func (f *fakeEngine) DefaultTrino(context.Context) (queryengine.Engine, error) { return f, nil }

func (f *fakeEngine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	return &fakeSession{f: f, spec: spec}, nil
}

func (f *fakeEngine) Cancel(context.Context, queryengine.QueryHandle) error {
	f.mu.Lock()
	f.killCalls++
	f.mu.Unlock()
	return nil
}

type fakeSession struct {
	f    *fakeEngine
	spec queryengine.SessionSpec
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	f := s.f
	f.mu.Lock()
	f.callCount++
	f.lastUser = s.spec.Identity.UserID
	f.sqls = append(f.sqls, sql)
	f.mu.Unlock()

	if f.ctasCount != "" {
		return emitBigints(sink, []string{f.ctasCount})
	}
	if f.assignID && cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{QueryID: testQueryID})
	}
	if f.err != nil {
		return f.err
	}
	if f.block > 0 {
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

// fakeS3 drains the upload stream and reports a fixed size or error.
type fakeS3 struct {
	bytes       int64
	err         error
	uploadCalls int
}

func (f *fakeS3) PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error) {
	f.uploadCalls++
	_, _ = io.Copy(io.Discard, r)
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

func execParams() ExecuteStatementParams {
	return ExecuteStatementParams{
		ExecutionID:  testExecID,
		Position:     1,
		SQL:          "SELECT 1",
		TrinoCatalog: "cat",
		RequestID:    "req-1",
		Timeout:      time.Minute,
	}
}

// TestExecuteStatement_Heartbeats pins the liveness fix: the activity records
// a heartbeat carrying the Trino query id as soon as the id is known.
func TestExecuteStatement_Heartbeats(t *testing.T) {
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{
		engines: &fakeEngine{assignID: true},
		s3:      &fakeS3{},
		cfg:     testActivitiesConfig(),
	}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	var mutext sync.Mutex
	var beats []heartbeatDetails
	ts.SetOnActivityHeartbeatListener(func(_ *activity.Info, details converter.EncodedValues) {
		var hb heartbeatDetails
		if err := details.Get(&hb); err == nil {
			mutext.Lock()
			beats = append(beats, hb)
			mutext.Unlock()
		}
	})
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})

	_, err := ts.ExecuteActivity(ExecuteStatement, execParams())
	if err != nil {
		t.Fatalf("unexpected activity error: %v", err)
	}

	mutext.Lock()
	defer mutext.Unlock()
	if len(beats) == 0 {
		t.Fatal("no heartbeat was recorded during statement execution")
	}
	if beats[0].TrinoQueryID != testQueryID {
		t.Fatalf("heartbeat query id = %q, want %q", beats[0].TrinoQueryID, testQueryID)
	}
}

// TestExecuteStatement_RetriesOnlyBeforeQueryID pins the duplicate-run fix: a
// transport failure before Trino accepted the statement is retried with a
// bounded backoff, never after a query id exists.
func TestExecuteStatement_RetriesOnlyBeforeQueryID(t *testing.T) {
	before := defaultActivities
	backoffBefore := append(statementTransportBackoff[:0:0], statementTransportBackoff...)
	statementTransportBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() {
		defaultActivities = before
		statementTransportBackoff = backoffBefore
	}()

	fake := &fakeEngine{err: queryengine.NewError(queryengine.CodeUnavailable, "trino", "", "connection refused")} // never assigns an id
	defaultActivities = &Activities{engines: fake, cfg: testActivitiesConfig()}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})

	result, err := ts.ExecuteActivity(ExecuteStatement, execParams())
	if err != nil {
		t.Fatalf("unexpected activity error: %v", err)
	}
	var r *ExecuteStatementResult
	if err := result.Get(&r); err != nil {
		t.Fatalf("decode activity result: %v", err)
	}
	if r.Error != fake.err.Error() {
		t.Fatalf("result error = %q, want %q", r.Error, fake.err.Error())
	}

	fake.mu.Lock()
	calls := fake.callCount
	fake.mu.Unlock()
	if calls != 4 { // initial + 3 bounded retries
		t.Fatalf("transport retries = %d, want 4 attempts", calls)
	}
}

// TestExecuteStatementCancellationIsCovered documents where the cancel path is
// pinned: the client abandons the Trino query (internal/trino
// TestExecute_CancelSendsDelete) and the workflow marks the statement and
// execution cancelled (internal/temporal/workflows
// TestWorkflow_CancelMarksCancelled). The activity's own cancel branch returns
// temporal.NewCanceledError(), exercised through that workflow, because a
// standalone activity test has no public way to build an activity context.
func TestExecuteStatementCancellationIsCovered(t *testing.T) {}

// TestExecuteStatement_Truncates pins the result-cap behavior: crossing the
// row cap stops the stream, kills the query, and returns a truncated (not
// failed) result.
func TestExecuteStatement_Truncates(t *testing.T) {
	before := defaultActivities
	defer func() { defaultActivities = before }()

	fake := &fakeEngine{assignID: true, deliver: 5}
	defaultActivities = &Activities{
		engines: fake,
		s3:      &fakeS3{},
		cfg:     &config.Config{ResultMaxRows: 2, ResultMaxBytes: 1 << 20},
	}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})

	result, err := ts.ExecuteActivity(ExecuteStatement, execParams())
	if err != nil {
		t.Fatalf("unexpected activity error: %v", err)
	}
	var r *ExecuteStatementResult
	if err := result.Get(&r); err != nil {
		t.Fatalf("decode activity result: %v", err)
	}
	if !r.Truncated {
		t.Fatalf("expected truncated=true, got %+v", r)
	}
	if r.RowCount != 2 {
		t.Fatalf("row count = %d, want 2 (the cap)", r.RowCount)
	}
	if r.Error != "" {
		t.Fatalf("truncation is not an error, got %q", r.Error)
	}
	fake.mu.Lock()
	kc := fake.killCalls
	fake.mu.Unlock()
	if kc == 0 {
		t.Fatal("the over-cap query must be killed")
	}
}

// TestExecuteStatement_DDLNoUpload pins that statements without a result schema
// (DDL/DML) upload nothing and carry no result key.
func TestExecuteStatement_DDLNoUpload(t *testing.T) {
	before := defaultActivities
	defer func() { defaultActivities = before }()

	s3 := &fakeS3{}
	defaultActivities = &Activities{
		engines: &fakeEngine{noColumns: true},
		s3:      s3,
		cfg:     testActivitiesConfig(),
	}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})

	result, err := ts.ExecuteActivity(ExecuteStatement, ExecuteStatementParams{
		ExecutionID: testExecID,
		Position:    1,
		SQL:         "CREATE TABLE x (a int)",
		Timeout:     time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected activity error: %v", err)
	}
	var r *ExecuteStatementResult
	if err := result.Get(&r); err != nil {
		t.Fatalf("decode activity result: %v", err)
	}
	if r.ResultKey != "" {
		t.Fatalf("DDL must not produce a result key, got %q", r.ResultKey)
	}
	if s3.uploadCalls != 0 {
		t.Fatalf("DDL must not upload anything, got %d uploads", s3.uploadCalls)
	}
}

// TestExecuteStatement_UploadFailureFails patches the upload-failure path: when
// the S3 upload fails the statement is reported as failed, never finished
// without a result.
func TestExecuteStatement_UploadFailureFails(t *testing.T) {
	before := defaultActivities
	defer func() { defaultActivities = before }()

	defaultActivities = &Activities{
		engines: &fakeEngine{assignID: true},
		s3:      &fakeS3{err: errors.New("s3 down")},
		cfg:     testActivitiesConfig(),
	}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})

	result, err := ts.ExecuteActivity(ExecuteStatement, execParams())
	if err != nil {
		t.Fatalf("unexpected activity error: %v", err)
	}
	var r *ExecuteStatementResult
	if err := result.Get(&r); err != nil {
		t.Fatalf("decode activity result: %v", err)
	}
	if r.Error == "" || !strings.Contains(r.Error, "store results") {
		t.Fatalf("expected a store-results error, got %q", r.Error)
	}
	if r.ResultKey != "" {
		t.Fatalf("a failed upload must not publish a result key, got %q", r.ResultKey)
	}
}

// TestExecuteStatement_TruncatesOnBytes: the byte cap counts what was written
// to the stored stream; past it the result is truncated and the query killed.
func TestExecuteStatement_TruncatesOnBytes(t *testing.T) {
	before := defaultActivities
	defer func() { defaultActivities = before }()

	fake := &fakeEngine{assignID: true, deliver: 50}
	defaultActivities = &Activities{
		engines: fake,
		s3:      &fakeS3{},
		cfg:     &config.Config{ResultMaxRows: 1000, ResultMaxBytes: 1024},
	}
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteStatement, activity.RegisterOptions{Name: "ExecuteStatement"})
	result, err := ts.ExecuteActivity(ExecuteStatement, execParams())
	if err != nil {
		t.Fatal(err)
	}
	var r *ExecuteStatementResult
	if err := result.Get(&r); err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || r.RowCount == 0 || r.RowCount >= 50 || r.Error != "" {
		t.Fatalf("result = %+v, want truncated with some rows", r)
	}
	if fake.killCalls == 0 {
		t.Fatal("the over-cap query must be killed")
	}
}
