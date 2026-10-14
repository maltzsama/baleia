package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/migrate"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// run migrations automatically on startup
	if err := migrate.Up(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
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
		return bootstrap.EnsureDefaultTrinoEngine(ctx, db.New(tx), cfg.TrinoURL)
	}); err != nil {
		slog.Error("default trino engine", "err", err)
		os.Exit(1)
	}

	// Credentials are sealed with BALEIA_ENCRYPTION_KEY (never JWT_SECRET). With
	// stored credentials and no key, refuse to start rather than serve rows we
	// cannot read.
	keyring, err := bootstrap.LoadKeyring(ctx, cfg, func(ctx context.Context) (int64, error) {
		var n int64
		err := pool.QueryRow(ctx, "SELECT count(*) FROM catalog_connections WHERE credentials_enc IS NOT NULL").Scan(&n)
		return n, err
	})
	if err != nil {
		slog.Error("credential keyring", "err", err)
		os.Exit(1)
	}

	srv := server.New(cfg, pool, keyring)
	if err := srv.Run(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
