package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// stmtRow is one query_statements row of statementStore.
type stmtRow struct {
	id     pgtype.UUID
	status string
	code   string
	handle []byte
	result string
}

// statementStore keeps an execution's statements in memory with the same
// guarded transitions as the SQL queries.
type statementStore struct {
	db.Querier
	mu        sync.Mutex
	rows      map[int32]*stmtRow
	execKey   string
	handleErr error
}

func newStatementStore(n int) *statementStore {
	st := &statementStore{rows: map[int32]*stmtRow{}}
	for i := int32(1); i <= int32(n); i++ {
		st.rows[i] = &stmtRow{id: pgtype.UUID{Bytes: [16]byte{byte(i)}, Valid: true}, status: "pending"}
	}
	return st
}

func (st *statementStore) status() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, len(st.rows))
	for pos, r := range st.rows {
		out[pos-1] = r.status
	}
	return out
}

func (st *statementStore) row(pos int32) stmtRow {
	st.mu.Lock()
	defer st.mu.Unlock()
	return *st.rows[pos]
}

func (st *statementStore) StartQueryStatement(_ context.Context, p db.StartQueryStatementParams) (int64, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if r := st.rows[p.Position]; r != nil && r.status == "pending" {
		r.status = "running"
		return 1, nil
	}
	return 0, nil
}

func (st *statementStore) ListQueryStatements(context.Context, pgtype.UUID) ([]db.QueryStatement, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []db.QueryStatement
	for pos, r := range st.rows {
		out = append(out, db.QueryStatement{ID: r.id, Position: pos, Status: r.status})
	}
	return out, nil
}

func (st *statementStore) UpdateQueryStatement(_ context.Context, p db.UpdateQueryStatementParams) (db.QueryStatement, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.rows {
		if r.id != p.ID {
			continue
		}
		if r.status != "running" {
			return db.QueryStatement{}, pgx.ErrNoRows
		}
		r.status = p.Status
		if p.ErrorCode != nil {
			r.code = *p.ErrorCode
		}
		if p.ResultKey != nil {
			r.result = *p.ResultKey
		}
		return db.QueryStatement{}, nil
	}
	return db.QueryStatement{}, pgx.ErrNoRows
}

func (st *statementStore) SetExecutionResult(_ context.Context, p db.SetExecutionResultParams) error {
	st.mu.Lock()
	st.execKey = *p.ResultKey
	st.mu.Unlock()
	return nil
}

func (st *statementStore) SetStatementQueryHandle(_ context.Context, p db.SetStatementQueryHandleParams) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if r := st.rows[p.Position]; r != nil && r.status == "running" {
		r.handle = p.QueryHandle
	}
	return st.handleErr
}

func (st *statementStore) SkipPendingStatements(context.Context, pgtype.UUID) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.rows {
		if r.status == "pending" {
			r.status = "skipped"
		}
	}
	return nil
}

func (st *statementStore) ListRunningStatementHandles(context.Context, pgtype.UUID) ([]db.ListRunningStatementHandlesRow, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []db.ListRunningStatementHandlesRow
	for pos, r := range st.rows {
		if r.status == "running" && r.handle != nil {
			out = append(out, db.ListRunningStatementHandlesRow{Position: pos, QueryHandle: r.handle})
		}
	}
	return out, nil
}

func (st *statementStore) AbandonRunningStatements(_ context.Context, p db.AbandonRunningStatementsParams) ([]db.AbandonRunningStatementsRow, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []db.AbandonRunningStatementsRow
	for pos, r := range st.rows {
		if r.status == "running" {
			r.status = p.Status
			if p.ErrorCode != nil {
				r.code = *p.ErrorCode
			}
			out = append(out, db.AbandonRunningStatementsRow{Position: pos, QueryHandle: r.handle})
		}
	}
	return out, nil
}

var testEngineID = pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}

func scriptParams(sqls ...string) ExecuteScriptParams {
	refs := make([]StatementRef, len(sqls))
	for i, s := range sqls {
		refs[i] = StatementRef{Position: int32(i + 1), SQL: s}
	}
	return ExecuteScriptParams{
		ExecutionID: testExecID,
		Target:      queryengine.Target{EngineID: testEngineID, EngineKind: "trino", EngineCatalog: "cat"},
		Identity:    queryengine.Identity{UserID: "u_1"},
		Statements:  refs,
		Timeout:     time.Minute,
		RequestID:   "req-1",
	}
}

// runScript runs ExecuteScript in a test activity environment.
func runScript(t *testing.T, a *Activities, p ExecuteScriptParams) (*ExecuteScriptResult, error) {
	t.Helper()
	before := defaultActivities
	t.Cleanup(func() { defaultActivities = before })
	defaultActivities = a
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteScript, activity.RegisterOptions{Name: "ExecuteScript"})
	v, err := ts.ExecuteActivity(ExecuteScript, p)
	if err != nil {
		return nil, err
	}
	var r *ExecuteScriptResult
	if err := v.Get(&r); err != nil {
		t.Fatal(err)
	}
	return r, nil
}

// TestExecuteScript_SessionStatePersists: every statement of an execution
// runs on ONE session, so session state set by one statement is visible to
// the next (it would not be with a session per statement).
func TestExecuteScript_SessionStatePersists(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	store := newStatementStore(2)
	r, err := runScript(t, &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()},
		scriptParams("SET x=42", "SELECT var x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Statements) != 2 || r.Statements[1].Status != "finished" || r.Statements[1].RowCount != 1 {
		t.Fatalf("outcomes = %+v", r.Statements)
	}
	if fake.sessions != 1 || fake.closed != 1 {
		t.Fatalf("sessions opened/closed = %d/%d, want 1/1", fake.sessions, fake.closed)
	}
	if got := store.status(); got[0] != "finished" || got[1] != "finished" {
		t.Fatalf("statuses = %v", got)
	}
	if store.row(2).result == "" || store.execKey != store.row(2).result {
		t.Fatalf("the execution's result must be the last statement's: %q vs %q", store.execKey, store.row(2).result)
	}
	if store.row(1).result != "" {
		t.Fatalf("SET has no result set, got key %q", store.row(1).result)
	}
}

// TestExecuteScript_StopsOnFailure: statement 2 fails with its engine code,
// 3 and 4 never run and are skipped.
func TestExecuteScript_StopsOnFailure(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino, failOn: "BROKEN"}
	store := newStatementStore(4)
	r, err := runScript(t, &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()},
		scriptParams("SELECT 1", "BROKEN", "SELECT 3", "SELECT 4"))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.status(); strings.Join(got, ",") != "finished,failed,skipped,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	if code := store.row(2).code; code != string(queryengine.CodeSyntax) {
		t.Fatalf("error code = %q, want syntax", code)
	}
	if len(r.Statements) != 2 || r.Statements[1].ErrorCode != "syntax" || r.Statements[1].Error == "" {
		t.Fatalf("outcomes = %+v", r.Statements)
	}
	if len(fake.sqls) != 2 {
		t.Fatalf("statements sent to the engine = %v", fake.sqls)
	}
}

// TestExecuteScript_RunsOnTarget: the session is opened on the target's
// engine with its catalog, schema, read-only flag and identity.
func TestExecuteScript_RunsOnTarget(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	p := scriptParams("SELECT 1")
	p.Target.EngineCatalog, p.Target.Schema, p.Target.ReadOnly = "sales", "eu", true
	if _, err := runScript(t, &Activities{queries: newStatementStore(1), engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()}, p); err != nil {
		t.Fatal(err)
	}
	if len(fake.gotEngine) != 1 || fake.gotEngine[0] != "e1000000-0000-0000-0000-000000000000" {
		t.Fatalf("engines asked for = %v", fake.gotEngine)
	}
	spec := fake.lastSpec
	if spec.Catalog != "sales" || len(spec.Schema) != 1 || spec.Schema[0] != "eu" || !spec.ReadOnly || spec.Identity.UserID != "u_1" || spec.Tags.ExecutionID != testExecID {
		t.Fatalf("session spec = %+v", spec)
	}
}

// TestExecuteScript_AgentSessionTag: an agent execution carries its agent id
// and is tagged as an agent session (#59).
func TestExecuteScript_AgentSessionTag(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	p := scriptParams("SELECT 1")
	p.Target.ReadOnly = true
	p.Identity.AgentID = "a-1"
	if _, err := runScript(t, &Activities{queries: newStatementStore(1), engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()}, p); err != nil {
		t.Fatal(err)
	}
	spec := fake.lastSpec
	if spec.Identity.AgentID != "a-1" || spec.Tags.Source != "agent" || !spec.ReadOnly {
		t.Fatalf("session spec = %+v", spec)
	}
}

// TestExecuteScript_NoEngineFailsFirst: a target without an engine never
// falls back to a default engine; the first statement fails, the rest skip.
func TestExecuteScript_NoEngineFailsFirst(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	store := newStatementStore(2)
	p := scriptParams("SELECT 1", "SELECT 2")
	p.Target.EngineID = pgtype.UUID{}
	if _, err := runScript(t, &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()}, p); err != nil {
		t.Fatal(err)
	}
	if got := store.status(); strings.Join(got, ",") != "failed,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	if len(fake.gotEngine) != 0 {
		t.Fatalf("no engine may be looked up: %v", fake.gotEngine)
	}
}

// TestExecuteScript_HeartbeatsHandle: the heartbeat carries the running
// statement's position and engine handle as soon as it is known, and the
// handle is persisted for cross-process cancel.
func TestExecuteScript_HeartbeatsHandle(t *testing.T) {
	store := newStatementStore(2)
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: store, engines: &fakeEngine{Engine: testTrino, assignID: true}, s3: &fakeS3{}, cfg: testActivitiesConfig()}

	var mu sync.Mutex
	var beats []heartbeatDetails
	recordBefore := recordHeartbeat
	defer func() { recordHeartbeat = recordBefore }()
	recordHeartbeat = func(ctx context.Context, details ...any) {
		mu.Lock()
		beats = append(beats, details[0].(heartbeatDetails))
		mu.Unlock()
	}
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteScript, activity.RegisterOptions{Name: "ExecuteScript"})
	if _, err := ts.ExecuteActivity(ExecuteScript, scriptParams("SELECT 1", "SELECT 2")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	withHandle := map[int32]bool{}
	for _, b := range beats {
		if b.Handle.QueryID == testQueryID {
			withHandle[b.Position] = true
		}
	}
	if !withHandle[1] || !withHandle[2] {
		t.Fatalf("heartbeats must carry each statement's handle: %+v", beats)
	}
	var h queryengine.QueryHandle
	if err := json.Unmarshal(store.row(1).handle, &h); err != nil || h.QueryID != testQueryID {
		t.Fatalf("persisted handle = %s", store.row(1).handle)
	}
}

// TestExecuteScript_HeartbeatsWhileBlocked: an engine that blocks without
// reporting progress (a direct database) still heartbeats.
func TestExecuteScript_HeartbeatsWhileBlocked(t *testing.T) {
	intervalBefore := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	defer func() { heartbeatInterval = intervalBefore }()
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: newStatementStore(1), engines: &fakeEngine{Engine: testTrino, block: 200 * time.Millisecond}, s3: &fakeS3{}, cfg: testActivitiesConfig()}

	var mu sync.Mutex
	beats := 0
	recordBefore := recordHeartbeat
	defer func() { recordHeartbeat = recordBefore }()
	recordHeartbeat = func(ctx context.Context, details ...any) {
		mu.Lock()
		beats++
		mu.Unlock()
		recordBefore(ctx, details...)
	}
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(ExecuteScript, activity.RegisterOptions{Name: "ExecuteScript"})
	if _, err := ts.ExecuteActivity(ExecuteScript, scriptParams("SELECT 1")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if beats < 5 {
		t.Fatalf("heartbeats while blocked = %d, want several", beats)
	}
}

// TestExecuteScript_RetriesOnlyBeforeHandle pins the duplicate-run fix: a
// transport failure before the engine accepted the statement is retried with
// a bounded backoff, never after a handle exists.
func TestExecuteScript_RetriesOnlyBeforeHandle(t *testing.T) {
	backoffBefore := statementTransportBackoff
	statementTransportBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { statementTransportBackoff = backoffBefore }()

	fake := &fakeEngine{Engine: testTrino, err: queryengine.NewError(queryengine.CodeUnavailable, "trino", "", "connection refused")}
	store := newStatementStore(1)
	r, err := runScript(t, &Activities{queries: store, engines: fake, cfg: testActivitiesConfig()}, scriptParams("INSERT INTO t VALUES (1)"))
	if err != nil {
		t.Fatal(err)
	}
	if fake.callCount != 4 { // initial + 3 bounded retries
		t.Fatalf("attempts = %d, want 4", fake.callCount)
	}
	if r.Statements[0].ErrorCode != string(queryengine.CodeUnavailable) {
		t.Fatalf("outcome = %+v", r.Statements[0])
	}

	fake = &fakeEngine{Engine: testTrino, assignID: true, err: queryengine.NewError(queryengine.CodeUnavailable, "trino", "", "connection reset")}
	if _, err := runScript(t, &Activities{queries: newStatementStore(1), engines: fake, cfg: testActivitiesConfig()}, scriptParams("INSERT INTO t VALUES (1)")); err != nil {
		t.Fatal(err)
	}
	if fake.callCount != 1 {
		t.Fatalf("a statement with a handle must not be retried: %d attempts", fake.callCount)
	}
}

// TestExecuteScript_Truncates: crossing the row cap stops the stream, cancels
// the statement and stores a truncated (not failed) result.
func TestExecuteScript_Truncates(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino, assignID: true, deliver: 5}
	r, err := runScript(t, &Activities{queries: newStatementStore(1), engines: fake, s3: &fakeS3{},
		cfg: &config.Config{ResultMaxRows: 2, ResultMaxBytes: 1 << 20}}, scriptParams("SELECT 1"))
	if err != nil {
		t.Fatal(err)
	}
	o := r.Statements[0]
	if o.Status != "finished" || !o.Truncated || o.RowCount != 2 || o.Error != "" {
		t.Fatalf("outcome = %+v", o)
	}
	if fake.killCalls == 0 {
		t.Fatal("the over-cap statement must be cancelled")
	}
}

// TestExecuteScript_TruncatesOnBytes: the byte cap counts what was written
// to the stored stream.
func TestExecuteScript_TruncatesOnBytes(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino, assignID: true, deliver: 50}
	r, err := runScript(t, &Activities{queries: newStatementStore(1), engines: fake, s3: &fakeS3{},
		cfg: &config.Config{ResultMaxRows: 1000, ResultMaxBytes: 1024}}, scriptParams("SELECT 1"))
	if err != nil {
		t.Fatal(err)
	}
	o := r.Statements[0]
	if !o.Truncated || o.RowCount == 0 || o.RowCount >= 50 || o.Error != "" {
		t.Fatalf("outcome = %+v", o)
	}
}

// TestExecuteScript_DDLNoUpload: statements without a result schema upload
// nothing and carry no result key.
func TestExecuteScript_DDLNoUpload(t *testing.T) {
	s3 := &fakeS3{}
	r, err := runScript(t, &Activities{queries: newStatementStore(1), engines: &fakeEngine{Engine: testTrino, noColumns: true}, s3: s3, cfg: testActivitiesConfig()},
		scriptParams("CREATE TABLE x (a int)"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Statements[0].ResultKey != "" || s3.uploadCalls != 0 {
		t.Fatalf("DDL stored a result: %+v, %d uploads", r.Statements[0], s3.uploadCalls)
	}
}

// TestExecuteScript_UploadFailureFails: a failed upload fails the statement,
// never finishes it without a result.
func TestExecuteScript_UploadFailureFails(t *testing.T) {
	store := newStatementStore(2)
	r, err := runScript(t, &Activities{queries: store, engines: &fakeEngine{Engine: testTrino, assignID: true}, s3: &fakeS3{err: errors.New("s3 down")}, cfg: testActivitiesConfig()},
		scriptParams("SELECT 1", "SELECT 2"))
	if err != nil {
		t.Fatal(err)
	}
	o := r.Statements[0]
	if o.Status != "failed" || !strings.Contains(o.Error, "store results") || o.ResultKey != "" {
		t.Fatalf("outcome = %+v", o)
	}
	if got := store.status(); strings.Join(got, ",") != "failed,skipped" {
		t.Fatalf("statuses = %v", got)
	}
}

// TestExecuteScript_ForbiddenStatement: the editor gate runs again before
// each statement (defense in depth) and fails it without reaching the engine.
func TestExecuteScript_ForbiddenStatement(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	store := newStatementStore(1)
	if _, err := runScript(t, &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()},
		scriptParams("CREATE CATALOG evil USING memory")); err != nil {
		t.Fatal(err)
	}
	if store.row(1).status != "failed" || store.row(1).code != codeEditorRejected || len(fake.sqls) != 0 {
		t.Fatalf("row = %+v, engine saw %v", store.row(1), fake.sqls)
	}
}

// scriptWorkflow runs ExecuteScript the way QueryExecutionWorkflowV2 does,
// so a test can cancel it through the workflow. The short heartbeat timeout
// makes the SDK send heartbeats (which deliver the cancel) without delay.
func scriptWorkflow(ctx workflow.Context, p ExecuteScriptParams) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		HeartbeatTimeout:    100 * time.Millisecond,
		WaitForCancellation: true,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, ExecuteScript, p).Get(ctx, nil)
}

// TestExecuteScript_CancelUsesHandle: cancelling the workflow cancels the
// script, which asks the engine to stop the running statement by its handle,
// marks it cancelled and skips the rest.
func TestExecuteScript_CancelUsesHandle(t *testing.T) {
	// Cancellation reaches an activity through its next heartbeat.
	intervalBefore := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	defer func() { heartbeatInterval = intervalBefore }()
	fake := &fakeEngine{Engine: testTrino, assignID: true, block: time.Minute, started: make(chan struct{})}
	store := newStatementStore(2)
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()}

	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterWorkflow(scriptWorkflow)
	env.RegisterActivity(ExecuteScript)
	done := make(chan struct{})
	go func() {
		env.ExecuteWorkflow(scriptWorkflow, scriptParams("SELECT 1", "SELECT 2"))
		close(done)
	}()
	<-fake.started
	env.CancelWorkflow()
	<-done
	// The test environment resolves a cancelled activity at once; wait for
	// the activity's own cleanup (it closes the session last).
	deadline := time.Now().Add(5 * time.Second)
	for {
		fake.mu.Lock()
		closed := fake.closed
		fake.mu.Unlock()
		if closed == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := store.status(); strings.Join(got, ",") != "cancelled,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.cancelled) != 1 || fake.cancelled[0].QueryID != testQueryID {
		t.Fatalf("engine cancels = %+v, want the running statement's handle", fake.cancelled)
	}
	if fake.closed != 1 {
		t.Fatalf("the session must be closed, closed=%d", fake.closed)
	}
}

// TestAbandonStatements_CancelsAndSkips: after a crash the statement still
// recorded as running is stopped on the engine by its persisted handle and
// ends interrupted; the rest are skipped. Running it twice changes nothing.
func TestAbandonStatements_CancelsAndSkips(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino}
	store := newStatementStore(3)
	store.rows[1].status = "finished"
	store.rows[2].status = "running"
	store.rows[2].handle = []byte(`{"engine_id":"e1","query_id":"q-2"}`)
	a := &Activities{queries: store, engines: fake}
	p := AbandonStatementsParams{ExecutionID: testExecID, EngineID: testEngineID, Status: "failed", ErrorCode: CodeInterrupted, Error: "interrupted"}
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = a
	ts.RegisterActivity(AbandonStatements)
	for range 2 {
		if _, err := ts.ExecuteActivity(AbandonStatements, p); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.status(); strings.Join(got, ",") != "finished,failed,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	if store.row(2).code != CodeInterrupted {
		t.Fatalf("code = %q", store.row(2).code)
	}
	if len(fake.cancelled) != 1 || fake.cancelled[0].QueryID != "q-2" {
		t.Fatalf("engine cancels = %+v", fake.cancelled)
	}
}

// TestExecuteScript_CancelledOnEngine: a statement the engine reports as
// cancelled (the cancel endpoint killed it by handle before the workflow's
// cancel arrived) ends cancelled, not failed, and the rest are skipped.
func TestExecuteScript_CancelledOnEngine(t *testing.T) {
	fake := &fakeEngine{Engine: testTrino, err: queryengine.NewError(queryengine.CodeCancelled, "trino", "ADMINISTRATIVELY_KILLED", "killed")}
	store := newStatementStore(2)
	r, err := runScript(t, &Activities{queries: store, engines: fake, s3: &fakeS3{}, cfg: testActivitiesConfig()}, scriptParams("SELECT 1", "SELECT 2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.status(); strings.Join(got, ",") != "cancelled,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	if len(r.Statements) != 1 || r.Statements[0].Status != "cancelled" {
		t.Fatalf("outcomes = %+v", r.Statements)
	}
}

// TestExecuteScript_DeadlineIsTimeout: when the script's own deadline
// expires mid-statement, the statement ends failed/timeout (not cancelled),
// is stopped on the engine, the rest are skipped, and its partial result is
// not stored.
func TestExecuteScript_DeadlineIsTimeout(t *testing.T) {
	intervalBefore := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	defer func() { heartbeatInterval = intervalBefore }()
	fake := &fakeEngine{Engine: testTrino, assignID: true, beginThenBlock: true, block: time.Minute, started: make(chan struct{})}
	store := newStatementStore(2)
	s3 := &fakeS3{}
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: store, engines: fake, s3: s3, cfg: testActivitiesConfig()}

	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterWorkflow(shortScriptWorkflow)
	env.RegisterActivity(ExecuteScript)
	env.ExecuteWorkflow(shortScriptWorkflow, scriptParams("SELECT 1", "SELECT 2"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		fake.mu.Lock()
		closed := fake.closed
		fake.mu.Unlock()
		if closed == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := store.status(); strings.Join(got, ",") != "failed,skipped" {
		t.Fatalf("statuses = %v", got)
	}
	if code := store.row(1).code; code != string(queryengine.CodeTimeout) {
		t.Fatalf("code = %q, want timeout", code)
	}
	fake.mu.Lock()
	cancels := len(fake.cancelled)
	fake.mu.Unlock()
	if cancels != 1 {
		t.Fatalf("engine cancels = %d, want 1", cancels)
	}
	s3.mu.Lock()
	defer s3.mu.Unlock()
	if len(s3.readErrs) != 1 || !errors.Is(s3.readErrs[0], errResultAborted) {
		t.Fatalf("the partial upload must fail, got %v", s3.readErrs)
	}
}

// shortScriptWorkflow runs ExecuteScript with a deadline shorter than the
// statement.
func shortScriptWorkflow(ctx workflow.Context, p ExecuteScriptParams) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 300 * time.Millisecond,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, ExecuteScript, p).Get(ctx, nil)
}
