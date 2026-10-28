package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/galileostudio/baleia/internal/app"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/schedulespecs"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const taskQueue = "baleia-task-queue"

func main() {
	cfg := config.Load()
	// An invalid level logs at info until Validate rejects it in run.
	level, _ := cfg.SlogLevel()
	// JSON like the api's (#379); the Temporal SDK logs through it too.
	logger := obs.NewLogger(os.Stdout, level)
	slog.SetDefault(logger)
	if err := run(cfg, logger); err != nil {
		logger.ErrorContext(context.Background(), "worker", "err", err)
		os.Exit(1)
	}
}

// run returns instead of exiting so every deferred Close runs.
func run(cfg *config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Setup is cancelled by SIGINT/SIGTERM like the API's (#367); the worker
	// itself stops on worker.InterruptCh below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	core, err := app.New(ctx, cfg, app.Options{Service: "worker", PoolSize: 10, Logger: logger})
	if err != nil {
		return err
	}
	defer core.Close()
	go obs.ServePprof(ctx, logger, cfg.PprofAddr)
	pool, reg := core.Pool, core.Metrics

	// The worker's own listener: /metrics (Temporal SDK, pool, build) and
	// /healthz. Prometheus scrapes it directly; it is never behind the JWT.
	if cfg.WorkerMetricsAddr != "" {
		srv := &http.Server{Addr: cfg.WorkerMetricsAddr, Handler: metricsMux(reg, cfg.MetricsToken), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.ErrorContext(ctx, "worker metrics listener", "addr", cfg.WorkerMetricsAddr, "err", err)
			}
		}()
		defer srv.Close()
	}

	w := worker.New(core.Temporal, taskQueue, worker.Options{Interceptors: obs.WorkerInterceptors()})

	// Lens reads the Trino coordinator's runtime state as the service identity.
	lensMonitor := monitor.New(trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoServiceUser, core.Dialer.HTTPClient(60*time.Second)))

	act := activities.New(logger, pool, cfg, core.Keyring, core.Connections, core.Engines, lensMonitor, core.Storage, core.Dialects)
	act.SetMetrics(reg)
	act.SetMetadataSyncer(metadata.NewSyncer(pool, metadata.NewSources(core.Queries, core.Keyring, core.Connections, core.Engines)))
	// The dispatcher observes federated engines' catalogs (adopting the ones
	// Baleia did not create) with the binder, as the catalog DDL identity.
	binder := core.Binder
	act.SetEngineObserver(func(ctx context.Context, eng db.Engine, clusterID pgtype.UUID) error {
		q := db.New(pool)
		l := bindings.Lifecycle{Store: q, Binder: binder.WithStore(q), Engine: bindings.EngineRef{ID: eng.ID, ClusterID: clusterID}, ConnectionID: eng.ConnectionID}
		if _, err := l.Observe(ctx); err != nil {
			return err
		}
		_, err := q.DisableMissingAdoptedBindings(ctx, eng.ID)
		return err
	})
	reg.WatchMetadataStaleness(logger, core.Queries.MaxMetadataStalenessSeconds)
	// Binding reconciliation (#37): managed drift only; adopted catalogs are
	// reported, never changed.
	act.SetEphemeralSweeper(func(ctx context.Context) (int, error) { return binder.SweepEphemeral(ctx, core.Queries) })
	projector := stored.Projector{Store: core.Queries, Keyring: core.Keyring, Providers: core.Connections, Binder: binder}
	act.SetEngineReconciler(func(ctx context.Context, eng db.Engine, clusterID pgtype.UUID, dryRun bool, onAction func(context.Context, bindings.Action)) (bindings.Report, error) {
		q := db.New(pool)
		r := bindings.Reconciler{
			Lifecycle:  bindings.Lifecycle{Store: q, Binder: binder.WithStore(q), Engine: bindings.EngineRef{ID: eng.ID, ClusterID: clusterID}, ConnectionID: eng.ConnectionID},
			Store:      q,
			Project:    projector.Project,
			MaxActions: cfg.ReconcileMaxActions,
			OnAction:   onAction,
		}
		return r.Run(ctx, dryRun)
	})
	act.SetAudit(core.Audit)

	// Audit export to OpenSearch (#50): disabled unless a URL is configured.
	if cfg.AuditExportURL != "" {
		sink, err := auditexport.NewHTTPBulk(auditexport.Config{
			URL: cfg.AuditExportURL, Index: cfg.AuditExportIndex, Auth: cfg.AuditExportAuth,
			User: cfg.AuditExportUser, Password: cfg.AuditExportPassword, Token: cfg.AuditExportToken, CAFile: cfg.AuditExportCAFile,
		}, obs.WrapHTTPClient(&http.Client{Timeout: 60 * time.Second}))
		if err != nil {
			return fmt.Errorf("audit export sink: %w", err)
		}
		pg := auditexport.Postgres{Queries: core.Queries}
		// Registered once: calling AuditExport() per lag report would register
		// the collectors again and panic.
		exportMetrics := reg.AuditExport()
		act.SetAuditExporter(&auditexport.Exporter{
			Source: pg, Cursor: pg, Sink: sink,
			Index: cfg.AuditExportIndex, Batch: cfg.AuditExportBatch, Lag: auditexport.DefaultSafetyLag,
			OnBatch: exportMetrics.Observe,
			OnLag:   func(t time.Time) { exportMetrics.SetLag(time.Since(t).Seconds()) },
		})
	}
	activities.Register(w, act)

	// V2 stays registered until its open runs drain; new runs start V3.
	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV2)
	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV3)
	w.RegisterWorkflow(workflows.LensQueryPollWorkflow)
	w.RegisterWorkflow(workflows.LensClusterPollWorkflow)
	w.RegisterWorkflow(workflows.LensTableHealthWorkflow)
	w.RegisterWorkflow(workflows.LensMaintenanceWorkflow)
	w.RegisterWorkflow(workflows.MaintenanceRunWorkflow)
	w.RegisterWorkflow(workflows.RetrieverDispatchWorkflow)
	w.RegisterWorkflow(workflows.RetrieverRefreshWorkflow)
	w.RegisterWorkflow(workflows.MetadataSyncWorkflow)
	w.RegisterWorkflow(workflows.MetadataSyncDispatchWorkflow)
	w.RegisterWorkflow(workflows.AuditPartitionsWorkflow)
	w.RegisterWorkflow(workflows.AuditExportWorkflow)
	w.RegisterWorkflow(workflows.CatalogReconcileWorkflow)
	w.RegisterWorkflow(workflows.DirectoryReconcileWorkflow)
	w.RegisterWorkflow(workflows.EphemeralCatalogSweepWorkflow)
	w.RegisterWorkflow(workflows.TokenCleanupWorkflow)

	if cfg.SchedulesEnabled {
		if err := ensureSchedules(ctx, core.Temporal, cfg); err != nil {
			return err
		}
		logger.InfoContext(ctx, "temporal schedules ensured")
	}

	logger.InfoContext(ctx, "worker started", "queue", taskQueue, "schedules_enabled", cfg.SchedulesEnabled)

	go serveHealth(ctx, logger, cfg.WorkerHealthPort)

	// One dry reconcile at boot: drift shows up in the log (counts only).
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		act.ReconcileAtBoot(ctx)
	}()

	return w.Run(worker.InterruptCh())
}

// ensureSchedules creates the recurring jobs as Temporal Schedules,
// idempotently on every boot. Each workflow does a single pass and returns.
func ensureSchedules(ctx context.Context, c client.Client, cfg *config.Config) error {
	if err := schedules.Ensure(ctx, c, taskQueue, schedulespecs.Core(cfg.MetadataSyncEvery())); err != nil {
		return fmt.Errorf("schedules: %w", err)
	}
	// Binding reconciliation on a schedule is opt-in (#37).
	var err error
	if cfg.ReconcileInterval > 0 {
		err = schedules.Ensure(ctx, c, taskQueue, []schedules.Spec{
			schedulespecs.CatalogReconcile(cfg.ReconcileInterval),
		})
	} else {
		err = schedules.Remove(ctx, c, schedules.CatalogReconcile)
	}
	if err != nil {
		return fmt.Errorf("reconcile schedule: %w", err)
	}
	// Audit export to OpenSearch (#50) runs only when a sink is configured.
	if cfg.AuditExportURL != "" {
		if err := schedules.Ensure(ctx, c, taskQueue, []schedules.Spec{
			schedulespecs.AuditExport(cfg.AuditExportInterval),
		}); err != nil {
			return fmt.Errorf("audit export schedule: %w", err)
		}
	} else {
		// Best effort: an absent schedule is the state we want.
		_ = schedules.Remove(ctx, c, schedules.AuditExport)
	}
	return nil
}

// serveHealth exposes a liveness endpoint Kubernetes can probe. If the port
// cannot be bound the worker still starts; the probe simply stays down.
func serveHealth(ctx context.Context, logger *slog.Logger, port string) {
	if _, err := strconv.Atoi(port); err != nil {
		logger.ErrorContext(ctx, "invalid health port", "port", port, "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.ErrorContext(ctx, "health server", "port", port, "err", err)
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
