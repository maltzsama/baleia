package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/migrate"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/galileostudio/baleia/internal/temporal/dial"
	"github.com/jackc/pgx/v5"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdownTracing, err := obs.Setup(ctx, cfg.TracingEnabled, "api")
	if err != nil {
		slog.Error("tracing setup", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	// run migrations automatically on startup
	if err := migrate.Up(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	reg := metrics.New("api")
	pool, err := dbpool.Open(ctx, cfg.DatabaseURL, dbpool.Options{
		MaxConns: cfg.PoolSize(20), MinConns: int32(cfg.DBMinConns), MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime, HealthCheckPeriod: cfg.DBHealthCheckPeriod,
		StatementTimeout: cfg.DBStatementTimeout, IdleInTxTimeout: cfg.DBIdleInTxTimeout, LockTimeout: cfg.DBLockTimeout,
		Tracer: obs.PGXTracer(reg.NewPoolTracer()),
	})
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Set the baleia_trino password from BALEIA_TRINO_DB_PASSWORD so the role and
	// the Trino plugin's connection stay in sync (no secret in a migration).
	if err := bootstrap.SetTrinoRolePassword(ctx, pool, cfg.TrinoDBPassword); err != nil {
		slog.Error("failed to set baleia_trino password", "err", err)
		os.Exit(1)
	}

	// Engine endpoints live in the database; TRINO_URL only seeds and updates
	// the default Trino engine Baleia created itself.
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return bootstrap.EnsureDefaultTrinoEngine(ctx, db.New(tx), bootstrap.DefaultTrinoEngine{
			URL: cfg.TrinoURL, ServiceUser: cfg.TrinoServiceUser, AdminUser: cfg.TrinoAdminUser,
		})
	}); err != nil {
		slog.Error("default trino engine", "err", err)
		os.Exit(1)
	}

	// Credentials are sealed with BALEIA_ENCRYPTION_KEY (never JWT_SECRET). With
	// stored credentials and no key, refuse to start rather than serve rows we
	// cannot read.
	keyring, err := bootstrap.LoadKeyring(ctx, cfg, db.New(pool).CountEncryptedConnections)
	if err != nil {
		slog.Error("credential keyring", "err", err)
		os.Exit(1)
	}

	registryKeyring, err := bootstrap.LoadRegistryKeyring(cfg)
	if err != nil {
		slog.Error("registry keyring", "err", err)
		os.Exit(1)
	}

	reg.WatchPool(pool)
	temporalMetrics, flushTemporal := reg.TemporalHandler()
	defer func() { _ = flushTemporal() }()
	temporalClient, err := dial.Temporal(ctx, cfg.TemporalHost, temporalMetrics, 30*time.Second, obs.ClientInterceptors())
	if err != nil {
		slog.Error("temporal", "err", err)
		os.Exit(1)
	}
	defer temporalClient.Close()

	srv, err := server.New(ctx, cfg, pool, keyring, registryKeyring, temporalClient, reg)
	if err != nil {
		slog.Error("server setup", "err", err)
		os.Exit(1)
	}
	if err := srv.Run(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
