package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/workflow"
)

func LensQueryPollerWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for {
		if err := workflow.ExecuteActivity(ctx, activities.LensPollTrinoQueries, activities.LensPollTrinoQueriesParams{}).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("poll trino queries failed", "err", err)
		}

		if err := workflow.Sleep(ctx, 30*time.Second); err != nil {
			return err
		}
	}
}

func LensClusterPollerWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		HeartbeatTimeout:    15 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for {
		if err := workflow.ExecuteActivity(ctx, activities.LensPollTrinoCluster, activities.LensPollTrinoClusterParams{}).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("poll trino cluster failed", "err", err)
		}

		if err := workflow.Sleep(ctx, 30*time.Second); err != nil {
			return err
		}
	}
}

func LensTableHealthWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for {
		if err := workflow.ExecuteActivity(ctx, activities.LensAnalyzeTableHealth, activities.LensAnalyzeTableHealthParams{}).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("analyze table health failed", "err", err)
		}

		if err := workflow.Sleep(ctx, 1*time.Hour); err != nil {
			return err
		}
	}
}
