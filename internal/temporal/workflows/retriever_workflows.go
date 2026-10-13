package workflows

import (
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RetrieverDispatchWorkflow is one pass of the due-retriever sweeper, driven by
// the retriever-scheduler Temporal Schedule. For every due retriever it starts
// a RetrieverRefreshWorkflow with a deterministic id (REJECT_DUPLICATE so two
// passes cannot start the same run) and advances the next run immediately, so a
// long refresh never bursts afterwards.
func RetrieverDispatchWorkflow(ctx workflow.Context) error {
	listCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	var due []activities.RetrieverDue
	if err := workflow.ExecuteActivity(listCtx, activities.RetrieverDispatch, activities.RetrieverDispatchParams{}).Get(listCtx, &due); err != nil {
		workflow.GetLogger(ctx).Error("retriever dispatch pass failed", "err", err)
		return err
	}

	advanceCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})

	for _, d := range due {
		workflowID := fmt.Sprintf("retriever-%s-%d", d.ID, d.NextRefreshAtUnix)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:               workflowID,
			TaskQueue:                "baleia-task-queue",
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowExecutionTimeout: time.Hour,
		})
		fut := workflow.ExecuteChildWorkflow(childCtx, RetrieverRefreshWorkflow, d.ID, "full")

		// Wait only for the child to be *started*. AlreadyStarted (REJECT
		// DUPLICATE) means another dispatch pass started this run; skip it.
		var we workflow.Execution
		if err := fut.GetChildWorkflowExecution().Get(childCtx, &we); err != nil {
			workflow.GetLogger(ctx).Info("retriever refresh already started",
				"retriever_id", d.ID, "err", err)
			continue
		}

		// Advance the schedule as soon as the run is *started* (issue #90), so
		// a long refresh never triggers a burst afterwards.
		if err := workflow.ExecuteActivity(advanceCtx, activities.AdvanceRetrieverSchedule, activities.AdvanceRetrieverScheduleParams{
			RetrieverID: d.ID,
		}).Get(advanceCtx, nil); err != nil {
			workflow.GetLogger(ctx).Warn("failed to advance retriever schedule",
				"retriever_id", d.ID, "err", err)
		}
	}

	return nil
}

// RetrieverRefreshWorkflow runs a single retriever refresh. Refreshes are never
// retried automatically: re-running an expensive materialization is not safe.
func RetrieverRefreshWorkflow(ctx workflow.Context, retrieverID, refreshType string) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	return workflow.ExecuteActivity(ctx, activities.RetrieverRefresh, activities.RetrieverRefreshParams{
		RetrieverID: retrieverID,
		RefreshType: refreshType,
	}).Get(ctx, nil)
}
