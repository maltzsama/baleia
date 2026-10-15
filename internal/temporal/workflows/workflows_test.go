package workflows

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const testStatementTimeout = 30 * time.Minute

// TestQueryExecutionWorkflowPropagatesRequestID proves the inbound HTTP
// correlation id reaches every downstream activity so a run can be joined back
// to the request log line.
func TestQueryExecutionWorkflowPropagatesRequestID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	const requestID = "req-abc-123"

	var execReqIDs []string

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1", "SELECT 2"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.MatchedBy(func(p activities.ExecuteStatementParams) bool {
		execReqIDs = append(execReqIDs, p.RequestID)
		return true
	})).Return(&activities.ExecuteStatementResult{
		ResultKey:  "query-results/exec-1/1/0.arrow",
		RowCount:   1,
		DurationMs: 1,
	}, nil).Times(2)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-1", "ws-1", "SELECT 1; SELECT 2", "cat", requestID, "u_123", testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, []string{requestID, requestID}, execReqIDs, "every statement execution carries the request id")
}

// TestQueryExecutionWorkflowEmptyRequestID covers the no-inbound-id path (a
// call that never passed through the HTTP middleware).
func TestQueryExecutionWorkflowEmptyRequestID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.Anything).
		Return(&activities.ExecuteStatementResult{RowCount: 1}, nil)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-2", "ws-1", "SELECT 1", "cat", "", "u_123", testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestWorkflow_NoRowsInPayloads documents the Temporal-carries-control contract:
// the statement activity result carries only metadata (a result object key),
// never rows, and that key is recorded on the statement row.
func TestWorkflow_ResultKeyPropagatesToStatement(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var gotUpdate *activities.UpdateStatementRecordParams

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.Anything).
		Return(&activities.ExecuteStatementResult{
			RowCount:    41,
			ResultKey:   "query-results/exec-5/1/0.arrow",
			ResultBytes: 1234,
			Truncated:   false,
			Columns:     []activities.ColumnResult{{Name: "c", Type: "bigint"}},
		}, nil)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.UpdateStatementRecordParams) error {
			c := p
			gotUpdate = &c
			return nil
		})
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-5", "ws-1", "SELECT 1", "cat", "req", "u_123", testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, gotUpdate)
	require.Equal(t, "query-results/exec-5/1/0.arrow", gotUpdate.ResultKey)
	require.Equal(t, int64(41), gotUpdate.RowsReturned)
	require.Equal(t, int64(1234), gotUpdate.ResultBytes)
	require.False(t, gotUpdate.Truncated)
}

// TestWorkflow_StatementNotRetried pins the data-corruption fix: a statement
// that errors is executed exactly once, never retried by Temporal. The
// workflow then marks the execution failed.
func TestWorkflow_StatementNotRetried(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var execCalls int32
	var failedExec *activities.FailExecutionParams

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"INSERT INTO t SELECT 1"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.ExecuteStatementParams) (*activities.ExecuteStatementResult, error) {
			atomic.AddInt32(&execCalls, 1)
			return &activities.ExecuteStatementResult{Error: "trino syntax error"}, nil
		})
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.FailExecutionParams) error {
			c := p
			failedExec = &c
			return nil
		})

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-3", "ws-1", "INSERT INTO t SELECT 1", "cat", "req", "u_123", testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, int32(1), atomic.LoadInt32(&execCalls), "a failed statement must not be re-executed")
	require.NotNil(t, failedExec, "the execution must be marked failed")
	require.NotEmpty(t, failedExec.ErrorMessage)
}

// TestWorkflow_CancelMarksCancelled pins the cancel flow: cancelling the
// workflow mid-statement marks the statement cancelled and never completes the
// execution as finished.
func TestWorkflow_CancelMarksCancelled(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	started := make(chan struct{})
	startedOnce := atomic.Bool{}
	var stmtCancelStatus string
	var completedStatus string

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.ExecuteStatementParams) (*activities.ExecuteStatementResult, error) {
			if startedOnce.CompareAndSwap(false, true) {
				close(started)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.UpdateStatementRecordParams) error {
			stmtCancelStatus = p.Status
			return nil
		})
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.CompleteExecutionParams) error {
			completedStatus = p.Status
			return nil
		})

	done := make(chan struct{})
	go func() {
		env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-4", "ws-1", "SELECT 1", "cat", "req", "u_123", testStatementTimeout)
		close(done)
	}()

	<-started
	env.CancelWorkflow()
	<-done

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "cancellation is not a workflow failure")
	require.Equal(t, "cancelled", stmtCancelStatus, "the in-flight statement must be marked cancelled")
	require.Equal(t, "cancelled", completedStatus, "the execution must not complete as finished")
}

func v2Input() QueryExecutionInput {
	return QueryExecutionInput{
		ExecutionID: "exec-2",
		WorkspaceID: "ws-1",
		SQL:         "SELECT 1; SELECT 2",
		Target: queryengine.Target{
			EngineID:      pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true},
			EngineKind:    "trino",
			EngineName:    "default",
			BindingID:     pgtype.UUID{Bytes: [16]byte{0xB1}, Valid: true},
			CatalogID:     pgtype.UUID{Bytes: [16]byte{0xC1}, Valid: true},
			CatalogName:   "Sales",
			EngineCatalog: "sales",
			Schema:        "eu",
			ReadOnly:      true,
		},
		Identity:         queryengine.Identity{UserID: "u_123"},
		StatementTimeout: testStatementTimeout,
		RequestID:        "req-v2",
	}
}

// TestQueryExecutionWorkflowV2_RunsOnTarget: every statement carries the
// target's engine, catalog, schema, read-only flag and dialect, and the
// script is split with that dialect.
func TestQueryExecutionWorkflowV2_RunsOnTarget(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var split activities.SplitSQLParams
	var stmts []activities.ExecuteStatementParams
	env.OnActivity(activities.SplitSQL, mock.Anything, mock.MatchedBy(func(p activities.SplitSQLParams) bool {
		split = p
		return true
	})).Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1", "SELECT 2"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.MatchedBy(func(p activities.ExecuteStatementParams) bool {
		stmts = append(stmts, p)
		return true
	})).Return(&activities.ExecuteStatementResult{RowCount: 1, DurationMs: 1}, nil).Times(2)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, "trino", split.Dialect)
	require.Len(t, stmts, 2)
	for i, p := range stmts {
		require.Equal(t, int32(i+1), p.Position)
		require.Equal(t, "e1000000-0000-0000-0000-000000000000", p.EngineID)
		require.Equal(t, "sales", p.TrinoCatalog)
		require.Equal(t, "eu", p.Schema)
		require.True(t, p.ReadOnly)
		require.Equal(t, "trino", p.Dialect)
		require.Equal(t, "u_123", p.User)
		require.Equal(t, "req-v2", p.RequestID)
		require.Equal(t, testStatementTimeout, p.Timeout)
	}
}

// TestV2_InputHasNoSecrets: the workflow input is recorded in Temporal
// history, so it must never carry connection secrets; the activity loads and
// decrypts the engine's connection itself.
func TestV2_InputHasNoSecrets(t *testing.T) {
	raw, err := json.Marshal(v2Input())
	require.NoError(t, err)
	for _, secret := range []string{"s3cret", "client_secret", "password", "token", "credentials"} {
		require.NotContains(t, strings.ToLower(string(raw)), secret)
	}
	// Structurally: no field of the input (or of Target/Identity) can hold
	// one. A new field must be reviewed here.
	fields := func(v any) []string {
		var out []string
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			out = append(out, typ.Field(i).Name)
		}
		return out
	}
	require.Equal(t, []string{"ExecutionID", "WorkspaceID", "SQL", "Target", "Identity", "StatementTimeout", "RequestID"}, fields(QueryExecutionInput{}))
	require.Equal(t, []string{"EngineID", "EngineKind", "EngineName", "BindingID", "CatalogID", "CatalogName", "EngineCatalog", "Schema", "ReadOnly"}, fields(queryengine.Target{}))
	require.Equal(t, []string{"UserID", "AgentID"}, fields(queryengine.Identity{}))
}

// TestQueryExecutionWorkflowV1_StillDefaultEngine: V1 statements name no
// engine, so they keep running on the default Trino engine.
func TestQueryExecutionWorkflowV1_StillDefaultEngine(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var p activities.ExecuteStatementParams
	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.MatchedBy(func(got activities.ExecuteStatementParams) bool {
		p = got
		return true
	})).Return(&activities.ExecuteStatementResult{}, nil)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-1", "ws-1", "SELECT 1", "cat", "req", "u_1", testStatementTimeout)
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "", p.EngineID)
	require.Equal(t, "", p.Dialect)
	require.Equal(t, "cat", p.TrinoCatalog)
}
