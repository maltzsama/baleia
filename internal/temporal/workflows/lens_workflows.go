package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// LensQueryPollWorkflow is one pass of the query-history poller. It is driven
// by a Temporal Schedule (see internal/temporal/schedules), so it must return
// after a single activity call.
func LensQueryPollWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	if err := workflow.ExecuteActivity(ctx, activities.LensPollTrinoQueries, activities.LensPollTrinoQueriesParams{}).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Error("poll trino queries failed", "err", err)
		return err
	}
	return nil
}

// LensClusterPollWorkflow is one pass of the cluster-stats poller.
func LensClusterPollWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		HeartbeatTimeout:    15 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	if err := workflow.ExecuteActivity(ctx, activities.LensPollTrinoCluster, activities.LensPollTrinoClusterParams{}).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Error("poll trino cluster failed", "err", err)
		return err
	}
	return nil
}

// LensTableHealthWorkflow scans enabled Iceberg-family catalogs and runs table
// health analysis per catalog, bounded to 4 concurrent calls. A failing
// catalog does not cancel the others.
func LensTableHealthWorkflow(ctx workflow.Context) error {
	listCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var catalogs []activities.EnabledIcebergCatalog
	if err := workflow.ExecuteActivity(listCtx, activities.ListEnabledIcebergCatalogs, activities.ListEnabledIcebergCatalogsParams{}).Get(listCtx, &catalogs); err != nil {
		workflow.GetLogger(ctx).Error("list enabled iceberg catalogs failed", "err", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	semCh := make(chan struct{}, 4)
	latch := workflow.NewWaitGroup(ctx)
	for _, cat := range catalogs {
		cat := cat
		latch.Add(1)
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer latch.Done()
			semCh <- struct{}{}
			defer func() { <-semCh }()

			actCtx := workflow.WithActivityOptions(ctx, ao)
			if err := workflow.ExecuteActivity(actCtx, activities.LensAnalyzeTableHealth, activities.LensAnalyzeTableHealthParams{
				CatalogConnectionID: cat.CatalogConnectionID,
				Catalog:             cat.Catalog,
			}).Get(actCtx, nil); err != nil {
				workflow.GetLogger(ctx).Error("analyze table health failed",
					"catalog", cat.Catalog, "err", err)
			}
		})
	}
	latch.Wait(ctx)
	passSucceeded(ctx, schedules.LensTableHealth)
	return nil
}

// passSucceeded records the end of a scheduled pass (baleia_schedule_last_
// success_timestamp_seconds). Versioned: runs started before it replay
// without the extra activity. A failed record is logged, not the pass's error.
func passSucceeded(ctx workflow.Context, schedule string) {
	if workflow.GetVersion(ctx, "schedule-pass-metric", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return
	}
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	if err := workflow.ExecuteActivity(actCtx, activities.SchedulePassSucceeded, activities.SchedulePassSucceededParams{Schedule: schedule}).Get(actCtx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("record schedule pass failed", "schedule", schedule, "err", err)
	}
}
