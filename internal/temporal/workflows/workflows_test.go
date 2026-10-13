package workflows

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
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
	var storeReqID string

	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1", "SELECT 2"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.ExecuteStatement, mock.Anything, mock.MatchedBy(func(p activities.ExecuteStatementParams) bool {
		execReqIDs = append(execReqIDs, p.RequestID)
		return true
	})).Return(&activities.ExecuteStatementResult{RowsReturned: 1, DurationMs: 1}, nil)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.StoreResults, mock.Anything, mock.MatchedBy(func(p activities.StoreResultsParams) bool {
		storeReqID = p.RequestID
		return true
	})).Return(&activities.StoreResultsResult{ResultKey: "query-results/exec-1/result.arrow"}, nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-1", "ws-1", "SELECT 1; SELECT 2", "cat", requestID, testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, []string{requestID, requestID}, execReqIDs, "every statement execution carries the request id")
	require.Equal(t, requestID, storeReqID, "the results upload carries the request id")
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
		Return(&activities.ExecuteStatementResult{RowsReturned: 1}, nil)
	env.OnActivity(activities.UpdateStatementRecord, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(activities.StoreResults, mock.Anything, mock.Anything).
		Return(&activities.StoreResultsResult{ResultKey: "k"}, nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-2", "ws-1", "SELECT 1", "cat", "", testStatementTimeout)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
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

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-3", "ws-1", "INSERT INTO t SELECT 1", "cat", "req", testStatementTimeout)

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
		env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-4", "ws-1", "SELECT 1", "cat", "req", testStatementTimeout)
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
