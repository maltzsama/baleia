package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/migrate"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func main() {
	cfg := config.Load()
	// An invalid level logs at info until Validate rejects it in run.
	level, _ := cfg.SlogLevel()
	logger := obs.NewLogger(os.Stdout, level)
	// Libraries that log through the default logger get the same output.
	slog.SetDefault(logger)
	// Cap the Go heap to the container's cgroup limit so the GC runs before
	// the OOM killer: Go otherwise targets the host's (effectively unlimited)
	// memory and grows until the kernel SIGKILLs the process.
	if _, err := memlimit.Set(); err != nil {
		// Not in a cgroup, or the cgroup has no memory limit: leave GOMEMLIMIT
		// unset (the pre-automemlimit behavior). Anything else is a real
		// misconfiguration worth surfacing.
		if errors.Is(err, memlimit.ErrNoCgroup) || errors.Is(err, memlimit.ErrNoLimit) {
			logger.Debug("no cgroup memory limit; GOMEMLIMIT left unset", "err", err)
		} else {
			logger.Warn("could not set GOMEMLIMIT from the cgroup", "err", err)
		}
	}

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
	// A Trino guard below 0.10.2 does not enforce baleia.catalogs (ADR 0018);
	// warn at startup, and let BALEIA_REQUIRE_CATALOG_SCOPE refuse instead.
	if msg := cfg.CatalogScopeWarning(); msg != "" {
		logger.ErrorContext(context.Background(), msg)
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
	go obs.ServePprof(ctx, logger, cfg.PprofAddr)

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
			Password: cfg.TrinoPassword != "", CA: core.TrinoCA, Seal: sealCredentials(core.Keyring),
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

// sealCredentials seals a connection's credentials with the keyring; nil when
// no keyring is configured (config.Validate refuses a Trino password then).
func sealCredentials(kr *crypto.Keyring) func([]byte, pgtype.UUID) (string, error) {
	if kr == nil {
		return nil
	}
	return func(plain []byte, id pgtype.UUID) (string, error) {
		return kr.Seal(plain, catalogs.CredentialsAAD(id))
	}
}
