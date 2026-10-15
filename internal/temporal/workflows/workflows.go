// Determinism: workflow code here never calls time.Now(), reads the
// environment, lets map iteration drive control flow, or starts goroutines.
// Everything it needs arrives in QueryExecutionInput.
package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// QueryExecutionInput is the V2 workflow input (#13). It carries control
// only: the activity loads the engine's connection and decrypts it, so no
// secret ever enters workflow history (TestV2_InputHasNoSecrets).
type QueryExecutionInput struct {
	ExecutionID      string
	WorkspaceID      string
	SQL              string
	Target           queryengine.Target
	Identity         queryengine.Identity
	StatementTimeout time.Duration
	RequestID        string
	// ExecutionTimeout caps the whole script (BALEIA_EXECUTION_TIMEOUT);
	// 0 = defaultExecutionTimeout.
	ExecutionTimeout time.Duration
}

// defaultExecutionTimeout caps a script when the input names no cap.
const defaultExecutionTimeout = 2 * time.Hour

// QueryExecutionWorkflowV2 runs every statement of a query on the target's
// engine, in ONE activity holding ONE engine session (#27), so session state
// (USE, SET, temporary tables, transactions) persists across statements on
// every engine. The workflow never names an engine: the target does.
//
// Do not change this workflow's commands in place once runs may be open:
// add V3 and drain V2 (see #27).
func QueryExecutionWorkflowV2(ctx workflow.Context, in QueryExecutionInput) error {
	dialectName := dialect.Trino
	if d, ok := dialect.ForEngineKind(in.Target.EngineKind); ok {
		dialectName = d.Name()
	}

	// Bookkeeping activities are idempotent DB updates and may be retried.
	bookkeepingCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})

	var split activities.SplitSQLResult
	if err := workflow.ExecuteActivity(bookkeepingCtx, activities.SplitSQL, activities.SplitSQLParams{
		SQL:     in.SQL,
		Dialect: dialectName,
	}).Get(bookkeepingCtx, &split); err != nil {
		return failExecution(bookkeepingCtx, in.ExecutionID, err)
	}

	if err := workflow.ExecuteActivity(bookkeepingCtx, activities.CreateStatementRecords, activities.CreateStatementRecordsParams{
		ExecutionID: in.ExecutionID,
		Statements:  split.Statements,
	}).Get(bookkeepingCtx, nil); err != nil {
		return failExecution(bookkeepingCtx, in.ExecutionID, err)
	}

	refs := make([]activities.StatementRef, len(split.Statements))
	for i, sql := range split.Statements {
		refs[i] = activities.StatementRef{Position: int32(i + 1), SQL: sql}
	}

	// The script is never retried automatically: a session cannot be resumed
	// and a retried INSERT or CREATE TABLE AS would write twice. On cancel the
	// workflow waits for the script to stop its statement on the engine.
	scriptCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: scriptTimeout(len(refs), in.StatementTimeout, in.ExecutionTimeout),
		HeartbeatTimeout:    30 * time.Second,
		WaitForCancellation: true,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var res activities.ExecuteScriptResult
	err := workflow.ExecuteActivity(scriptCtx, activities.ExecuteScript, activities.ExecuteScriptParams{
		ExecutionID: in.ExecutionID,
		Target:      in.Target,
		Identity:    in.Identity,
		Statements:  refs,
		Timeout:     in.StatementTimeout,
		RequestID:   in.RequestID,
	}).Get(scriptCtx, &res)

	// A cancelled workflow (UI cancel) cancels the script, which stops the
	// statement on the engine. Record it on a disconnected context.
	if temporal.IsCanceledError(err) {
		return markCancelled(ctx, in)
	}
	// The script stopped without an answer (worker crash, heartbeat or
	// script timeout): the statement it was running is interrupted.
	if err != nil {
		_ = workflow.ExecuteActivity(bookkeepingCtx, activities.AbandonStatements, activities.AbandonStatementsParams{
			ExecutionID: in.ExecutionID,
			EngineID:    in.Target.EngineID,
			Status:      "failed",
			ErrorCode:   activities.CodeInterrupted,
			Error:       "the worker running the statement stopped; its outcome on the engine is unknown",
		}).Get(bookkeepingCtx, nil)
		workflow.GetLogger(ctx).Warn("script interrupted", "execution_id", in.ExecutionID, "err", err)
		return failExecution(bookkeepingCtx, in.ExecutionID, err)
	}

	var totalRows, totalDurationMs int64
	for _, o := range res.Statements {
		switch o.Status {
		case "failed":
			return failExecution(bookkeepingCtx, in.ExecutionID, nil)
		case "cancelled":
			// Stopped on the engine; the cancel endpoint already recorded the
			// execution's cancellation, this is a no-op then.
			return workflow.ExecuteActivity(bookkeepingCtx, activities.CompleteExecution, activities.CompleteExecutionParams{
				ExecutionID: in.ExecutionID,
				Status:      "cancelled",
			}).Get(bookkeepingCtx, nil)
		}
		totalRows += o.RowCount
		totalDurationMs += o.DurationMs
	}
	return workflow.ExecuteActivity(bookkeepingCtx, activities.CompleteExecution, activities.CompleteExecutionParams{
		ExecutionID:  in.ExecutionID,
		Status:       "finished",
		RowsReturned: totalRows,
		DurationMs:   totalDurationMs,
	}).Get(bookkeepingCtx, nil)
}

// scriptTimeout bounds the script activity: every statement may take its
// whole timeout, capped by the execution timeout.
func scriptTimeout(statements int, statementTimeout, executionTimeout time.Duration) time.Duration {
	if executionTimeout <= 0 {
		executionTimeout = defaultExecutionTimeout
	}
	t := time.Duration(statements) * statementTimeout
	if t <= 0 || t > executionTimeout {
		return executionTimeout
	}
	return t
}

// failExecution marks the execution failed with a bounded-retry bookkeeping
// context and returns the cause (nil when a statement failed: the run itself
// completed).
func failExecution(ctx workflow.Context, executionID string, cause error) error {
	params := activities.FailExecutionParams{ExecutionID: executionID}
	if cause != nil {
		params.ErrorMessage = cause.Error()
	}
	_ = workflow.ExecuteActivity(ctx, activities.FailExecution, params).Get(ctx, nil)
	return cause
}

// markCancelled ends the statement that was running as cancelled, skips the
// rest and records the execution's cancellation. It runs on a disconnected
// context because the workflow context is already cancelled.
func markCancelled(ctx workflow.Context, in QueryExecutionInput) error {
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	dctx = workflow.WithActivityOptions(dctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})

	_ = workflow.ExecuteActivity(dctx, activities.AbandonStatements, activities.AbandonStatementsParams{
		ExecutionID: in.ExecutionID,
		EngineID:    in.Target.EngineID,
		Status:      "cancelled",
		ErrorCode:   string(queryengine.CodeCancelled),
	}).Get(dctx, nil)

	// The cancel endpoint already flipped the execution to cancelled; this is
	// a no-op in that case (the update is conditional on status='running').
	_ = workflow.ExecuteActivity(dctx, activities.CompleteExecution, activities.CompleteExecutionParams{
		ExecutionID: in.ExecutionID,
		Status:      "cancelled",
	}).Get(dctx, nil)
	return nil
}
