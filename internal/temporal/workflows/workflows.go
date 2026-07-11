package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/workflow"
)

func QueryExecutionWorkflow(ctx workflow.Context, executionID, workspaceID, sql, trinoCatalog string) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var splitResult activities.SplitSQLResult
	if err := workflow.ExecuteActivity(ctx, activities.SplitSQL, activities.SplitSQLParams{
		SQL: sql,
	}).Get(ctx, &splitResult); err != nil {
		_ = workflow.ExecuteActivity(ctx, activities.FailExecution, activities.FailExecutionParams{
			ExecutionID:  executionID,
			ErrorMessage: err.Error(),
		}).Get(ctx, nil)
		return err
	}

	if err := workflow.ExecuteActivity(ctx, activities.CreateStatementRecords, activities.CreateStatementRecordsParams{
		ExecutionID: executionID,
		Statements:  splitResult.Statements,
	}).Get(ctx, nil); err != nil {
		_ = workflow.ExecuteActivity(ctx, activities.FailExecution, activities.FailExecutionParams{
			ExecutionID:  executionID,
			ErrorMessage: err.Error(),
		}).Get(ctx, nil)
		return err
	}

	var totalRows int64
	var totalDurationMs int64
	var allColumns []activities.ColumnResult
	var allRows [][]interface{}

	for i, stmt := range splitResult.Statements {
		position := int32(i + 1)

		var execResult activities.ExecuteStatementResult
		err := workflow.ExecuteActivity(ctx, activities.ExecuteStatement, activities.ExecuteStatementParams{
			ExecutionID:  executionID,
			Position:     position,
			SQL:          stmt,
			TrinoCatalog: trinoCatalog,
		}).Get(ctx, &execResult)

		if err != nil || execResult.Error != "" {
			errMsg := execResult.Error
			if err != nil {
				errMsg = err.Error()
			}

			_ = workflow.ExecuteActivity(ctx, activities.UpdateStatementRecord, activities.UpdateStatementRecordParams{
				ExecutionID:  executionID,
				Position:     position,
				Status:       "failed",
				ErrorMessage: errMsg,
				DurationMs:   execResult.DurationMs,
			}).Get(ctx, nil)

			_ = workflow.ExecuteActivity(ctx, activities.FailExecution, activities.FailExecutionParams{
				ExecutionID:  executionID,
				ErrorMessage: errMsg,
			}).Get(ctx, nil)
			if err != nil {
				return err
			}
			return nil
		}

		_ = workflow.ExecuteActivity(ctx, activities.UpdateStatementRecord, activities.UpdateStatementRecordParams{
			ExecutionID:  executionID,
			Position:     position,
			Status:       "finished",
			TrinoQueryID: execResult.TrinoQueryID,
			RowsReturned: execResult.RowsReturned,
			DurationMs:   execResult.DurationMs,
		}).Get(ctx, nil)

		totalRows += execResult.RowsReturned
		totalDurationMs += execResult.DurationMs
		allColumns = execResult.Columns
		allRows = append(allRows, execResult.Rows...)
	}

	_ = workflow.ExecuteActivity(ctx, activities.StoreResults, activities.StoreResultsParams{
		ExecutionID: executionID,
		Columns:     allColumns,
		Rows:        allRows,
	}).Get(ctx, nil)

	return workflow.ExecuteActivity(ctx, activities.CompleteExecution, activities.CompleteExecutionParams{
		ExecutionID:  executionID,
		Status:       "finished",
		RowsReturned: totalRows,
		DurationMs:   totalDurationMs,
	}).Get(ctx, nil)
}
