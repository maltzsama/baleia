package workflows

import (
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

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

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-1", "ws-1", "SELECT 1; SELECT 2", "cat", requestID)

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

	env.ExecuteWorkflow(QueryExecutionWorkflow, "exec-2", "ws-1", "SELECT 1", "cat", "")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
