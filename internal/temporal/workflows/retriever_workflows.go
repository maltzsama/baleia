package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/workflow"
)

func RetrieverSchedulerWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for {
		if err := workflow.ExecuteActivity(ctx, activities.RetrieverPoll, activities.RetrieverPollParams{}).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("retriever poll failed", "err", err)
		}

		if err := workflow.Sleep(ctx, 60*time.Second); err != nil {
			return err
		}
	}
}

func RetrieverRefreshWorkflow(ctx workflow.Context, retrieverID, refreshType string) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	return workflow.ExecuteActivity(ctx, activities.RetrieverRefresh, activities.RetrieverRefreshParams{
		RetrieverID: retrieverID,
		RefreshType: refreshType,
	}).Get(ctx, nil)
}
