package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// conflictBackoffTotal is the longest a run waits between commit-conflict
// retries (30s + 2m + 5m), added to its own timeout.
const conflictBackoffTotal = 8 * time.Minute

// MaintenanceOperationWorkflow runs one recorded maintenance run: a single
// operation with its frozen settings (#500). The activity records every
// outcome on the run.
func MaintenanceOperationWorkflow(ctx workflow.Context, runID string, timeoutSeconds int64) error {
	timeout := time.Duration(max(timeoutSeconds, 60)) * time.Second
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: timeout + conflictBackoffTotal,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, activities.RunMaintenanceOperation,
		activities.RunMaintenanceOperationParams{RunID: runID}).Get(ctx, nil)
}

// MaintenanceRoundWorkflow plans one catalog's round and starts each run as
// its own workflow, abandoned by the round: a long compaction must not hold
// the hourly health pass. A run whose workflow does not start is closed.
func MaintenanceRoundWorkflow(ctx workflow.Context, catalogID string) error {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var runs []activities.PlannedRun
	if err := workflow.ExecuteActivity(actCtx, activities.PlanMaintenanceRound,
		activities.PlanMaintenanceRoundParams{CatalogID: catalogID}).Get(actCtx, &runs); err != nil {
		return err
	}
	for _, run := range runs {
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:        run.WorkflowID,
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		})
		child := workflow.ExecuteChildWorkflow(childCtx, MaintenanceOperationWorkflow, run.RunID, run.TimeoutSeconds)
		if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("maintenance run did not start", "run", run.RunID, "err", err)
			_ = workflow.ExecuteActivity(actCtx, activities.FailMaintenanceRun,
				activities.FailMaintenanceRunParams{RunID: run.RunID, Message: err.Error()}).Get(actCtx, nil)
		}
	}
	return nil
}

// TableHealthAnalyzeWorkflow is "analyze now" (#500): one catalog's or one
// table's health pass, then the catalog's maintenance round.
func TableHealthAnalyzeWorkflow(ctx workflow.Context, params activities.LensAnalyzeTableHealthParams) error {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	if err := workflow.ExecuteActivity(actCtx, activities.LensAnalyzeTableHealth, params).Get(actCtx, nil); err != nil {
		return err
	}
	return workflow.ExecuteChildWorkflow(ctx, MaintenanceRoundWorkflow, params.CatalogID).Get(ctx, nil)
}
