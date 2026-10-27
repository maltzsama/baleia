package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/migrate"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/jackc/pgx/v5"
)

func main() {
	cfg := config.Load()
	// An invalid level logs at info until Validate rejects it in run.
	level, _ := cfg.SlogLevel()
	logger := obs.NewLogger(os.Stdout, level)
	// Libraries that log through the default logger get the same output.
	slog.SetDefault(logger)

	if err := run(cfg, logger); err != nil {
		logger.ErrorContext(context.Background(), "api", "err", err)
		os.Exit(1)
	}
}

// run returns instead of exiting so every deferred Close runs.
func run(cfg *config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Migrations run on their own connection, before the pool opens.
	if err := migrate.Up(ctx, logger, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migration: %w", err)
	}

	core, err := app.New(ctx, cfg, app.Options{Service: "api", PoolSize: 20, Logger: logger})
	if err != nil {
		return err
	}
	defer core.Close()

	// Set the baleia_trino password from BALEIA_TRINO_DB_PASSWORD so the role and
	// the Trino plugin's connection stay in sync (no secret in a migration).
	if err := bootstrap.SetTrinoRolePassword(ctx, core.Pool, cfg.TrinoDBPassword); err != nil {
		return fmt.Errorf("baleia_trino password: %w", err)
	}

	// Engine endpoints live in the database; TRINO_URL only seeds and updates
	// the default Trino engine Baleia created itself.
	if err := pgx.BeginFunc(ctx, core.Pool, func(tx pgx.Tx) error {
		return bootstrap.EnsureDefaultTrinoEngine(ctx, logger, db.New(tx), bootstrap.DefaultTrinoEngine{
			URL: cfg.TrinoURL, ServiceUser: cfg.TrinoServiceUser, AdminUser: cfg.TrinoAdminUser,
		})
	}); err != nil {
		return fmt.Errorf("default trino engine: %w", err)
	}

	srv, err := server.New(ctx, core)
	if err != nil {
		return fmt.Errorf("server setup: %w", err)
	}
	return srv.Run(ctx)
}
