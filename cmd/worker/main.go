package main

import (
	"context"
	"errors"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/temporal/dial"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	// Engine kinds register themselves.
	_ "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"go.temporal.io/sdk/worker"
)

const taskQueue = "baleia-task-queue"

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	reg := metrics.New("worker")
	pool, err := dbpool.Open(context.Background(), cfg.DatabaseURL, dbpool.Options{
		MaxConns: cfg.PoolSize(10), MinConns: int32(cfg.DBMinConns), MaxConnLifetime: cfg.DBMaxConnLifetime,
		Tracer: reg.NewPoolTracer(),
	})
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	reg.WatchPool(pool)

	temporalMetrics, flushTemporal := reg.TemporalHandler()
	defer func() { _ = flushTemporal() }()
	c, err := dial.Temporal(context.Background(), cfg.TemporalHost, temporalMetrics, 30*time.Second)
	if err != nil {
		slog.Error("temporal", "err", err)
		os.Exit(1)
	}
	defer c.Close()

	// The worker's own listener: /metrics (Temporal SDK, pool, build) and
	// /healthz. Prometheus scrapes it directly; it is never behind the JWT.
	if cfg.WorkerMetricsAddr != "" {
		srv := &http.Server{Addr: cfg.WorkerMetricsAddr, Handler: metricsMux(reg, cfg.MetricsToken), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("worker metrics listener", "addr", cfg.WorkerMetricsAddr, "err", err)
			}
		}()
		defer srv.Close()
	}

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

	// Every connection provider dials through one SSRF-safe dialer
	// (docs/adr/0011).
	dialer, err := connections.NewDialer(connections.DialerOptions{
		DenyCIDRs:     cfg.ConnectionsDenyCIDRs,
		AllowCIDRs:    cfg.ConnectionsAllowCIDRs,
		AllowLoopback: cfg.ConnectionsAllowLoopback,
	})
	if err != nil {
		slog.Error("invalid connection network settings", "err", err)
		os.Exit(1)
	}
	conns, err := providers.New(dialer, providers.Options{
		SQLite: sqlite.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite},
	})
	if err != nil {
		slog.Error("connection providers", "err", err)
		os.Exit(1)
	}

	// Statements run on engines (docs/adr/0002); Lens reads the Trino
	// coordinator's runtime state as the service identity.
	engines := queryengine.NewManager(db.New(pool), keyring, conns, dialer)
	lensMonitor := monitor.New(trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoServiceUser, dialer.HTTPClient(60*time.Second)))

	act := activities.New(pool, cfg, keyring, conns, engines, lensMonitor)
	act.SetMetrics(reg)
	activities.Register(w, act)

	w.RegisterWorkflow(workflows.QueryExecutionWorkflow) // V1: drains runs started before V2 (#27 removes it)
	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV2)
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
			{ID: schedules.LensQueryPoller, Workflow: workflows.LensQueryPollWorkflow, Every: 30 * time.Second},
			{ID: schedules.LensClusterPoller, Workflow: workflows.LensClusterPollWorkflow, Every: 30 * time.Second},
			{ID: schedules.LensTableHealth, Workflow: workflows.LensTableHealthWorkflow, Every: time.Hour},
			{ID: schedules.RetrieverScheduler, Workflow: workflows.RetrieverDispatchWorkflow, Every: 60 * time.Second},
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

// metricsMux serves the worker's /metrics and /healthz.
func metricsMux(reg *metrics.Registry, token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler(token))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}
