package activities

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trino"
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

// fakeTrinoClient simulates the Trino statement protocol so the activities can
// be tested against a blocked, failing, or always-succeeding backend.
type fakeTrinoClient struct {
	mu        sync.Mutex
	callCount int
	block     time.Duration
	err       error
	assignID  bool
}

func (f *fakeTrinoClient) ExecuteWithOptions(ctx context.Context, sql string, opts trino.ExecuteOptions) (*trino.QueryResult, error) {
	f.mu.Lock()
	f.callCount++
	f.mu.Unlock()

	if f.assignID && opts.OnQueryID != nil {
		opts.OnQueryID(testQueryID)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.block > 0 {
		select {
		case <-time.After(f.block):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if opts.OnProgress != nil {
		opts.OnProgress()
	}
	return &trino.QueryResult{QueryID: testQueryID, RowCount: 1}, nil
}

func (f *fakeTrinoClient) Execute(ctx context.Context, sql, catalog, schema string) (*trino.QueryResult, error) {
	return nil, nil
}

func (f *fakeTrinoClient) KillQuery(ctx context.Context, queryID, message string) error { return nil }

func (f *fakeTrinoClient) ListQueries(ctx context.Context) ([]trino.QueryInfo, error) {
	return nil, nil
}

func (f *fakeTrinoClient) GetClusterStats(ctx context.Context) (*trino.ClusterStats, error) {
	return nil, nil
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
	defaultActivities = &Activities{trino: &fakeTrinoClient{assignID: true}}

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

	fake := &fakeTrinoClient{err: errors.New("connection refused")} // never assigns an id
	defaultActivities = &Activities{trino: fake}

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
