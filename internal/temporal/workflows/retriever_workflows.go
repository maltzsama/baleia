package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RetrieverDispatchWorkflow is one pass of the due-retriever sweeper, driven by
// the retriever-scheduler Temporal Schedule. Full dispatch semantics land with
// #90; today it polls for due retrievers so the schedule is live.
func RetrieverDispatchWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	if err := workflow.ExecuteActivity(ctx, activities.RetrieverPoll, activities.RetrieverPollParams{}).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Error("retriever dispatch pass failed", "err", err)
		return err
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
