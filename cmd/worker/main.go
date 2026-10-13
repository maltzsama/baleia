package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const taskQueue = "baleia-task-queue"

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	c, err := client.Dial(client.Options{HostPort: cfg.TemporalHost})
	if err != nil {
		slog.Error("failed to connect to temporal", "err", err)
		os.Exit(1)
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{})

	act := activities.New(pool, cfg)
	activities.Register(w, act)

	w.RegisterWorkflow(workflows.QueryExecutionWorkflow)
	w.RegisterWorkflow(workflows.LensQueryPollWorkflow)
	w.RegisterWorkflow(workflows.LensClusterPollWorkflow)
	w.RegisterWorkflow(workflows.LensTableHealthWorkflow)
	w.RegisterWorkflow(workflows.LensMaintenanceWorkflow)
	w.RegisterWorkflow(workflows.RetrieverDispatchWorkflow)
	w.RegisterWorkflow(workflows.RetrieverRefreshWorkflow)

	if cfg.SchedulesEnabled {
		// Recurring jobs are Temporal Schedules, created idempotently on every
		// boot. Each workflow does a single pass and returns.
		if err := schedules.Ensure(context.Background(), c, taskQueue, []schedules.Spec{
			{ID: "lens-query-poller", Workflow: workflows.LensQueryPollWorkflow, Every: 30 * time.Second},
			{ID: "lens-cluster-poller", Workflow: workflows.LensClusterPollWorkflow, Every: 30 * time.Second},
			{ID: "lens-table-health", Workflow: workflows.LensTableHealthWorkflow, Every: time.Hour},
			{ID: "retriever-scheduler", Workflow: workflows.RetrieverDispatchWorkflow, Every: 60 * time.Second},
		}); err != nil {
			slog.Error("failed to ensure schedules", "err", err)
			os.Exit(1)
		}
		slog.Info("temporal schedules ensured")
	}

	slog.Info("worker started", "queue", taskQueue, "schedules_enabled", cfg.SchedulesEnabled)
	if err := w.Run(worker.InterruptCh()); err != nil {
		slog.Error("worker error", "err", err)
		os.Exit(1)
	}
}