package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/glue"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	// Engine kinds register themselves.
	_ "github.com/galileostudio/baleia/internal/queryengine/clickhouse"
	_ "github.com/galileostudio/baleia/internal/queryengine/flightsql"
	_ "github.com/galileostudio/baleia/internal/queryengine/mysql"
	_ "github.com/galileostudio/baleia/internal/queryengine/oracle"
	_ "github.com/galileostudio/baleia/internal/queryengine/postgresql"
	sqliteengine "github.com/galileostudio/baleia/internal/queryengine/sqlite"
	_ "github.com/galileostudio/baleia/internal/queryengine/sqlserver"
	_ "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/dial"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/schedulespecs"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/worker"
)

const taskQueue = "baleia-task-queue"

func main() {
	// Setup is cancelled by SIGINT/SIGTERM like the API's (#367); the worker
	// itself stops on worker.InterruptCh below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	shutdownTracing, err := obs.Setup(ctx, cfg.TracingEnabled, "worker")
	if err != nil {
		slog.Error("tracing setup", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	reg := metrics.New("worker")
	pool, err := dbpool.Open(ctx, cfg.DatabaseURL, dbpool.Options{
		MaxConns: cfg.PoolSize(10), MinConns: int32(cfg.DBMinConns), MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime, HealthCheckPeriod: cfg.DBHealthCheckPeriod,
		StatementTimeout: cfg.DBStatementTimeout, IdleInTxTimeout: cfg.DBIdleInTxTimeout, LockTimeout: cfg.DBLockTimeout,
		Tracer: obs.PGXTracer(reg.NewPoolTracer()),
	})
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	reg.WatchPool(pool)

	temporalMetrics, flushTemporal := reg.TemporalHandler()
	defer func() { _ = flushTemporal() }()
	c, err := dial.Temporal(ctx, cfg.TemporalHost, temporalMetrics, 30*time.Second, obs.ClientInterceptors())
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

	w := worker.New(c, taskQueue, worker.Options{Interceptors: obs.WorkerInterceptors()})

	// The worker opens stored connection credentials (Lens table health), so it
	// follows the same key policy as the API: refuse to start without a key
	// once credentials exist.
	keyring, err := bootstrap.LoadKeyring(ctx, cfg, db.New(pool).CountEncryptedConnections)
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
		Glue:   glue.Options{AllowAmbientCredentials: cfg.AllowAmbientAWSCredentials, AllowInsecureEndpoint: cfg.ConnectionsAllowLoopback},
	})
	if err != nil {
		slog.Error("connection providers", "err", err)
		os.Exit(1)
	}
	// The SQLite engine confines files to the same root the provider uses
	// (docs/adr/0011).
	sqliteengine.Configure(sqliteengine.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite})

	// Statements run on engines (docs/adr/0002); Lens reads the Trino
	// coordinator's runtime state as the service identity.
	engines := queryengine.NewManager(db.New(pool), keyring, conns, dialer)
	lensMonitor := monitor.New(trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoServiceUser, dialer.HTTPClient(60*time.Second)))

	s3Client, err := storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		slog.Error("result storage", "err", err)
		os.Exit(1)
	}
	act := activities.New(pool, cfg, keyring, conns, engines, lensMonitor, s3Client)
	act.SetMetrics(reg)
	act.SetMetadataSyncer(metadata.NewSyncer(pool, metadata.NewSources(db.New(pool), keyring, conns, engines)))
	// The dispatcher observes federated engines' catalogs (adopting the ones
	// Baleia did not create) with the binder, as the catalog DDL identity.
	trinoAdmin := trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoAdminUser, dialer.HTTPClient(60*time.Second))
	registryKeyring, err := bootstrap.LoadRegistryKeyring(cfg)
	if err != nil {
		slog.Error("registry keyring", "err", err)
		os.Exit(1)
	}
	binder := bindtrino.New(trinoAdmin, db.New(pool)).WithRegistryKeyring(registryKeyring)
	act.SetEngineObserver(func(ctx context.Context, eng db.Engine, clusterID pgtype.UUID) error {
		q := db.New(pool)
		l := bindings.Lifecycle{Store: q, Binder: binder.WithStore(q), Engine: bindings.EngineRef{ID: eng.ID, ClusterID: clusterID}, ConnectionID: eng.ConnectionID}
		if _, err := l.Observe(ctx); err != nil {
			return err
		}
		_, err := q.DisableMissingAdoptedBindings(ctx, eng.ID)
		return err
	})
	reg.WatchMetadataStaleness(db.New(pool).MaxMetadataStalenessSeconds)
	// Binding reconciliation (#37): managed drift only; adopted catalogs are
	// reported, never changed.
	act.SetEphemeralSweeper(func(ctx context.Context) (int, error) { return binder.SweepEphemeral(ctx, db.New(pool)) })
	projector := stored.Projector{Store: db.New(pool), Keyring: keyring, Providers: conns, Binder: binder}
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
	act.SetAudit(audit.NewPostgres(db.New(pool), conns.SecretKeys(), reg.Audit().WriteFailed))

	// Audit export to OpenSearch (#50): disabled unless a URL is configured.
	if cfg.AuditExportURL != "" {
		sink, err := auditexport.NewHTTPBulk(auditexport.Config{
			URL: cfg.AuditExportURL, Index: cfg.AuditExportIndex, Auth: cfg.AuditExportAuth,
			User: cfg.AuditExportUser, Password: cfg.AuditExportPassword, Token: cfg.AuditExportToken, CAFile: cfg.AuditExportCAFile,
		}, obs.WrapHTTPClient(&http.Client{Timeout: 60 * time.Second}))
		if err != nil {
			slog.Error("audit export sink", "err", err)
			os.Exit(1)
		}
		pg := auditexport.Postgres{Queries: db.New(pool)}
		act.SetAuditExporter(&auditexport.Exporter{
			Source: pg, Cursor: pg, Sink: sink,
			Index: cfg.AuditExportIndex, Batch: cfg.AuditExportBatch, Lag: auditexport.DefaultSafetyLag,
			OnBatch: reg.AuditExport().Observe,
			OnLag:   func(t time.Time) { reg.AuditExport().SetLag(time.Since(t).Seconds()) },
		})
	}
	activities.Register(w, act)

	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV2)
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
		// Recurring jobs are Temporal Schedules, created idempotently on every
		// boot. Each workflow does a single pass and returns.
		if err := schedules.Ensure(ctx, c, taskQueue, schedulespecs.Core(cfg.MetadataSyncEvery())); err != nil {
			slog.Error("failed to ensure schedules", "err", err)
			os.Exit(1)
		}
		// Binding reconciliation on a schedule is opt-in (#37).
		if cfg.ReconcileInterval > 0 {
			err = schedules.Ensure(ctx, c, taskQueue, []schedules.Spec{
				schedulespecs.CatalogReconcile(cfg.ReconcileInterval),
			})
		} else {
			err = schedules.Remove(ctx, c, schedules.CatalogReconcile)
		}
		if err != nil {
			slog.Error("failed to ensure the reconcile schedule", "err", err)
			os.Exit(1)
		}
		slog.Info("temporal schedules ensured")
	}
	// Audit export to OpenSearch (#50) runs only when a sink is configured.
	if cfg.SchedulesEnabled {
		if cfg.AuditExportURL != "" {
			if err := schedules.Ensure(ctx, c, taskQueue, []schedules.Spec{
				schedulespecs.AuditExport(cfg.AuditExportInterval),
			}); err != nil {
				slog.Error("failed to ensure the audit export schedule", "err", err)
				os.Exit(1)
			}
		} else {
			// Best effort: an absent schedule is the state we want.
			_ = schedules.Remove(ctx, c, schedules.AuditExport)
		}
	}

	slog.Info("worker started", "queue", taskQueue, "schedules_enabled", cfg.SchedulesEnabled)

	go serveHealth(cfg.WorkerHealthPort)

	// One dry reconcile at boot: drift shows up in the log (counts only).
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		act.ReconcileAtBoot(ctx)
	}()

	if err := w.Run(worker.InterruptCh()); err != nil {
		slog.Error("worker error", "err", err)
		os.Exit(1)
	}
}

// serveHealth exposes a liveness endpoint Kubernetes can probe. If the port
// cannot be bound the worker still starts; the probe simply stays down.
func serveHealth(port string) {
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
