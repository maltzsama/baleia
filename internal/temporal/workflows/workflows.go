package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// QueryExecutionWorkflow runs every statement of a query sequentially against
// Trino, stopping on the first error. It carries control only; result rows live
// in object storage (#89).
//
// statementTimeout bounds each statement. It is passed as input because
// workflow code must stay deterministic and never read the environment.
func QueryExecutionWorkflow(ctx workflow.Context, executionID, workspaceID, sql, trinoCatalog, requestID, user string, statementTimeout time.Duration) error {
	// Bookkeeping activities are idempotent DB updates and may be retried.
	bookkeepingCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})

	// Statement execution is never retried automatically: a retried INSERT or
	// CREATE TABLE AS would write twice.
	statementCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: statementTimeout,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	// (Removed the EnsureCatalog activity call — CR-017 §I3/T1.)
	// With the baleia-trino-catalog-store plugin, Trino loads all catalogs
	// from trino_catalog_registry at boot. The write-through flow of the
	// CreateCatalog handler + the plugin's addOrReplaceCatalog callback
	// cover both boot-time and runtime catalog enrollment.

	var splitResult activities.SplitSQLResult
	if err := workflow.ExecuteActivity(bookkeepingCtx, activities.SplitSQL, activities.SplitSQLParams{
		SQL: sql,
	}).Get(bookkeepingCtx, &splitResult); err != nil {
		return failExecution(bookkeepingCtx, executionID, err)
	}

	if err := workflow.ExecuteActivity(bookkeepingCtx, activities.CreateStatementRecords, activities.CreateStatementRecordsParams{
		ExecutionID: executionID,
		Statements:  splitResult.Statements,
	}).Get(bookkeepingCtx, nil); err != nil {
		return failExecution(bookkeepingCtx, executionID, err)
	}

	var totalRows int64
	var totalDurationMs int64

	for i, stmt := range splitResult.Statements {
		position := int32(i + 1)

		var execResult activities.ExecuteStatementResult
		err := workflow.ExecuteActivity(statementCtx, activities.ExecuteStatement, activities.ExecuteStatementParams{
			ExecutionID:  executionID,
			Position:     position,
			SQL:          stmt,
			TrinoCatalog: trinoCatalog,
			RequestID:    requestID,
			User:         user,
			Timeout:      statementTimeout,
		}).Get(statementCtx, &execResult)

		// A cancelled workflow (UI cancel, timeout) cancels the statement
		// activity, which kills the Trino query. Record the cancellation on a
		// disconnected context and stop.
		if temporal.IsCanceledError(err) {
			return markCancelled(ctx, executionID, position)
		}

		if err != nil || execResult.Error != "" {
			errMsg := execResult.Error
			if err != nil {
				errMsg = err.Error()
			}

			_ = workflow.ExecuteActivity(bookkeepingCtx, activities.UpdateStatementRecord, activities.UpdateStatementRecordParams{
				ExecutionID:  executionID,
				Position:     position,
				Status:       "failed",
				ErrorMessage: errMsg,
				DurationMs:   execResult.DurationMs,
			}).Get(bookkeepingCtx, nil)

			_ = workflow.ExecuteActivity(bookkeepingCtx, activities.FailExecution, activities.FailExecutionParams{
				ExecutionID:  executionID,
				ErrorMessage: errMsg,
			}).Get(bookkeepingCtx, nil)
			if err != nil {
				return err
			}
			return nil
		}

		_ = workflow.ExecuteActivity(bookkeepingCtx, activities.UpdateStatementRecord, activities.UpdateStatementRecordParams{
			ExecutionID:  executionID,
			Position:     position,
			Status:       "finished",
			TrinoQueryID: execResult.TrinoQueryID,
			RowsReturned: execResult.RowCount,
			DurationMs:   execResult.DurationMs,
			ResultKey:    execResult.ResultKey,
			ResultBytes:  execResult.ResultBytes,
			Truncated:    execResult.Truncated,
		}).Get(bookkeepingCtx, nil)

		totalRows += execResult.RowCount
		totalDurationMs += execResult.DurationMs
	}

	return workflow.ExecuteActivity(bookkeepingCtx, activities.CompleteExecution, activities.CompleteExecutionParams{
		ExecutionID:  executionID,
		Status:       "finished",
		RowsReturned: totalRows,
		DurationMs:   totalDurationMs,
	}).Get(bookkeepingCtx, nil)
}

// failExecution marks the execution failed with a bounded-retry bookkeeping
// context and returns the original error.
func failExecution(ctx workflow.Context, executionID string, cause error) error {
	_ = workflow.ExecuteActivity(ctx, activities.FailExecution, activities.FailExecutionParams{
		ExecutionID:  executionID,
		ErrorMessage: cause.Error(),
	}).Get(ctx, nil)
	return cause
}

// markCancelled records the cancellation of the in-flight statement and the
// execution. It runs on a disconnected context because the workflow context is
// already cancelled by the time it is called.
func markCancelled(ctx workflow.Context, executionID string, position int32) error {
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	dctx = workflow.WithActivityOptions(dctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})

	_ = workflow.ExecuteActivity(dctx, activities.UpdateStatementRecord, activities.UpdateStatementRecordParams{
		ExecutionID: executionID,
		Position:    position,
		Status:      "cancelled",
	}).Get(dctx, nil)

	// The cancel endpoint already flipped the execution to cancelled; this is
	// a no-op in that case (the update is conditional on status='running').
	_ = workflow.ExecuteActivity(dctx, activities.CompleteExecution, activities.CompleteExecutionParams{
		ExecutionID: executionID,
		Status:      "cancelled",
	}).Get(dctx, nil)

	return nil
}
