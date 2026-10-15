package workflows

import (
	"context"
	"encoding/json"
	"errors"
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
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

const testStatementTimeout = 30 * time.Minute

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

// bookkeeping mocks the split and record activities for a script of n
// statements.
func bookkeeping(env *testsuite.TestWorkflowEnvironment, n int) {
	stmts := make([]string, n)
	for i := range stmts {
		stmts[i] = "SELECT " + string(rune('1'+i))
	}
	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).Return(&activities.SplitSQLResult{Statements: stmts}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).Return(nil)
}

func finished(n int) *activities.ExecuteScriptResult {
	r := &activities.ExecuteScriptResult{}
	for i := 1; i <= n; i++ {
		r.Statements = append(r.Statements, activities.StatementOutcome{Position: int32(i), Status: "finished", RowCount: 1, DurationMs: 2})
	}
	return r
}

// TestV2_RunsScriptOnTarget: the script is split with the target engine's
// dialect and runs in ONE activity on the target, with every statement in
// order; the totals complete the execution.
func TestV2_RunsScriptOnTarget(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var split activities.SplitSQLParams
	var script activities.ExecuteScriptParams
	var completed activities.CompleteExecutionParams
	env.OnActivity(activities.SplitSQL, mock.Anything, mock.MatchedBy(func(p activities.SplitSQLParams) bool {
		split = p
		return true
	})).Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1", "SELECT 2"}}, nil)
	env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.MatchedBy(func(p activities.ExecuteScriptParams) bool {
		script = p
		return true
	})).Return(finished(2), nil).Once()
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.MatchedBy(func(p activities.CompleteExecutionParams) bool {
		completed = p
		return true
	})).Return(nil)

	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, "trino", split.Dialect)
	in := v2Input()
	require.Equal(t, in.ExecutionID, script.ExecutionID)
	require.Equal(t, in.Target, script.Target)
	require.Equal(t, in.Identity, script.Identity)
	require.Equal(t, testStatementTimeout, script.Timeout)
	require.Equal(t, "req-v2", script.RequestID)
	require.Equal(t, []activities.StatementRef{{Position: 1, SQL: "SELECT 1"}, {Position: 2, SQL: "SELECT 2"}}, script.Statements)
	require.Equal(t, activities.CompleteExecutionParams{ExecutionID: "exec-2", Status: "finished", RowsReturned: 2, DurationMs: 4}, completed)
}

// TestV2_TwoEnginesSameWorkflow: the workflow never names an engine. Targets
// on engines of different kinds run through the same workflow, each split
// with its engine's dialect and executed on its engine.
func TestV2_TwoEnginesSameWorkflow(t *testing.T) {
	pg := v2Input()
	pg.Target.EngineID = pgtype.UUID{Bytes: [16]byte{0xE2}, Valid: true}
	pg.Target.EngineKind = "postgresql"
	for _, in := range []QueryExecutionInput{v2Input(), pg} {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		var dialectName string
		var engineID pgtype.UUID
		env.OnActivity(activities.SplitSQL, mock.Anything, mock.MatchedBy(func(p activities.SplitSQLParams) bool {
			dialectName = p.Dialect
			return true
		})).Return(&activities.SplitSQLResult{Statements: []string{"SELECT 1"}}, nil)
		env.OnActivity(activities.CreateStatementRecords, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(activities.ExecuteScript, mock.Anything, mock.MatchedBy(func(p activities.ExecuteScriptParams) bool {
			engineID = p.Target.EngineID
			return true
		})).Return(finished(1), nil)
		env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(QueryExecutionWorkflowV2, in)
		require.NoError(t, env.GetWorkflowError())
		require.Equal(t, in.Target.EngineKind, dialectName)
		require.Equal(t, in.Target.EngineID, engineID)
	}
}

// TestV2_PropagatesRequestID: the inbound HTTP correlation id reaches the
// script so a run can be joined back to the request log line.
func TestV2_PropagatesRequestID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 2)
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.MatchedBy(func(p activities.ExecuteScriptParams) bool {
		return p.RequestID == "req-v2"
	})).Return(finished(2), nil).Once()
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestV2_StatementFailureFailsExecution: a failed statement fails the
// execution; the run itself completes (the failure is the user's SQL).
func TestV2_StatementFailureFailsExecution(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 2)
	res := finished(1)
	res.Statements = append(res.Statements, activities.StatementOutcome{Position: 2, Status: "failed", ErrorCode: "syntax", Error: "syntax error"})
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).Return(res, nil)
	var failed bool
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).Return(func(context.Context, activities.FailExecutionParams) error {
		failed = true
		return nil
	})
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, failed, "the execution must be marked failed")
}

// TestV2_CancelledOnEngineCompletesCancelled: a statement cancelled on the
// engine ends the execution cancelled, neither finished nor failed.
func TestV2_CancelledOnEngineCompletesCancelled(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 2)
	res := &activities.ExecuteScriptResult{Statements: []activities.StatementOutcome{{Position: 1, Status: "cancelled", ErrorCode: "cancelled"}}}
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).Return(res, nil)
	var status string
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(func(_ context.Context, p activities.CompleteExecutionParams) error {
		status = p.Status
		return nil
	})
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "cancelled", status)
}

// TestV2_ScriptNotRetried pins the duplicate-write fix: a failed script is
// never re-executed automatically.
func TestV2_ScriptNotRetried(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 1)
	var calls int32
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).
		Return(func(context.Context, activities.ExecuteScriptParams) (*activities.ExecuteScriptResult, error) {
			atomic.AddInt32(&calls, 1)
			return nil, errors.New("worker lost")
		})
	env.OnActivity(activities.AbandonStatements, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// TestV2_HeartbeatTimeoutMarksInterrupted: when the worker running the
// script dies, the statement recorded as running ends failed/interrupted,
// the engine is asked to stop it (AbandonStatements uses the persisted
// handle), the rest are skipped and the execution fails.
func TestV2_HeartbeatTimeoutMarksInterrupted(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 3)
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).
		Return(nil, temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_HEARTBEAT, nil))
	var abandon activities.AbandonStatementsParams
	env.OnActivity(activities.AbandonStatements, mock.Anything, mock.MatchedBy(func(p activities.AbandonStatementsParams) bool {
		abandon = p
		return true
	})).Return(nil).Once()
	var failed bool
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).Return(func(context.Context, activities.FailExecutionParams) error {
		failed = true
		return nil
	})
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, "failed", abandon.Status)
	require.Equal(t, activities.CodeInterrupted, abandon.ErrorCode)
	require.Equal(t, v2Input().Target.EngineID, abandon.EngineID)
	require.Equal(t, "exec-2", abandon.ExecutionID)
	require.True(t, failed, "the execution must be marked failed")
}

// TestV2_CancelMarksCancelled: cancelling the workflow mid-script abandons
// the running statement as cancelled and records the execution's
// cancellation, never a completion.
func TestV2_CancelMarksCancelled(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 2)
	started := make(chan struct{})
	var once atomic.Bool
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, _ activities.ExecuteScriptParams) (*activities.ExecuteScriptResult, error) {
			if once.CompareAndSwap(false, true) {
				close(started)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
	var abandonStatus, completedStatus string
	env.OnActivity(activities.AbandonStatements, mock.Anything, mock.Anything).
		Return(func(_ context.Context, p activities.AbandonStatementsParams) error {
			abandonStatus = p.Status
			return nil
		})
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).
		Return(func(_ context.Context, p activities.CompleteExecutionParams) error {
			completedStatus = p.Status
			return nil
		})

	done := make(chan struct{})
	go func() {
		env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
		close(done)
	}()
	<-started
	env.CancelWorkflow()
	<-done

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "cancellation is not a workflow failure")
	require.Equal(t, "cancelled", abandonStatus)
	require.Equal(t, "cancelled", completedStatus)
}

// TestV2_RejectedScriptFails: a script the editor gate rejects fails the
// execution before any statement record exists.
func TestV2_RejectedScriptFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.OnActivity(activities.SplitSQL, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("forbidden", "editor_rejected", nil))
	var failed bool
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).Return(func(context.Context, activities.FailExecutionParams) error {
		failed = true
		return nil
	})
	env.ExecuteWorkflow(QueryExecutionWorkflowV2, v2Input())
	require.Error(t, env.GetWorkflowError())
	require.True(t, failed)
}

func TestScriptTimeout(t *testing.T) {
	require.Equal(t, 90*time.Minute, scriptTimeout(3, 30*time.Minute, 2*time.Hour))
	require.Equal(t, 2*time.Hour, scriptTimeout(10, 30*time.Minute, 2*time.Hour), "capped by the execution timeout")
	require.Equal(t, defaultExecutionTimeout, scriptTimeout(10, 30*time.Minute, 0), "no cap given: the default cap")
	require.Equal(t, time.Hour, scriptTimeout(1, 0, time.Hour), "no statement timeout: the cap")
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
	require.Equal(t, []string{"ExecutionID", "WorkspaceID", "SQL", "Target", "Identity", "StatementTimeout", "RequestID", "ExecutionTimeout"}, fields(QueryExecutionInput{}))
	require.Equal(t, []string{"EngineID", "EngineKind", "EngineName", "BindingID", "CatalogID", "CatalogName", "EngineCatalog", "Schema", "ReadOnly"}, fields(queryengine.Target{}))
	require.Equal(t, []string{"UserID", "AgentID"}, fields(queryengine.Identity{}))
}

// TestV2_Replay replays histories recorded on a real cluster (minikube, one
// per outcome: finished, a failed statement, cancelled, worker interrupted).
// A change to the workflow's commands fails here: add V3 instead (#27).
func TestV2_Replay(t *testing.T) {
	for _, name := range []string{"finished", "failed", "cancelled", "interrupted"} {
		t.Run(name, func(t *testing.T) {
			r := worker.NewWorkflowReplayer()
			r.RegisterWorkflow(QueryExecutionWorkflowV2)
			require.NoError(t, r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/v2_"+name+".json"))
		})
	}
}
