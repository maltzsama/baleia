package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

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

	w := worker.New(c, "baleia-task-queue", worker.Options{})

	act := activities.New(pool, cfg)
	activities.Register(w, act)

	w.RegisterWorkflow(workflows.QueryExecutionWorkflow)
	w.RegisterWorkflow(workflows.LensQueryPollerWorkflow)
	w.RegisterWorkflow(workflows.LensClusterPollerWorkflow)
	w.RegisterWorkflow(workflows.LensTableHealthWorkflow)
	w.RegisterWorkflow(workflows.LensMaintenanceWorkflow)
	w.RegisterWorkflow(workflows.RetrieverSchedulerWorkflow)
	w.RegisterWorkflow(workflows.RetrieverRefreshWorkflow)

	slog.Info("worker started", "queue", "baleia-task-queue")
	if err := w.Run(worker.InterruptCh()); err != nil {
		slog.Error("worker error", "err", err)
		os.Exit(1)
	}
}
