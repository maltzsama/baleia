package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/bootstrap"
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

	// The worker opens stored connection credentials (Lens table health), so it
	// follows the same key policy as the API: refuse to start without a key
	// once credentials exist.
	keyring, err := bootstrap.LoadKeyring(context.Background(), cfg, func(ctx context.Context) (int64, error) {
		var n int64
		err := pool.QueryRow(ctx, "SELECT count(*) FROM catalog_connections WHERE credentials_enc IS NOT NULL").Scan(&n)
		return n, err
	})
	if err != nil {
		slog.Error("credential keyring", "err", err)
		os.Exit(1)
	}

	act := activities.New(pool, cfg, keyring)
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

	go serveHealth("WORKER_HEALTH_PORT", "8081")

	if err := w.Run(worker.InterruptCh()); err != nil {
		slog.Error("worker error", "err", err)
		os.Exit(1)
	}
}

// serveHealth exposes a liveness endpoint Kubernetes can probe. If the port
// cannot be bound the worker still starts; the probe simply stays down.
func serveHealth(envKey, fallback string) {
	port := os.Getenv(envKey)
	if port == "" {
		port = fallback
	}
	if _, err := strconv.Atoi(port); err != nil {
		slog.Error("invalid health port", "port", port, "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("health server", "port", port, "err", err)
	}
}