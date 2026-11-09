// Package command owns the process lifecycle the API and worker binaries share:
// configuration, logging, the cgroup heap cap, signal handling and the shared
// dependency graph. Each binary keeps its own domain wiring.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/obs"
)

// Main runs a service's process lifecycle and exits non-zero on error. It
// loads the configuration, installs the logger, caps the heap, and then calls
// Run, which validates the configuration and calls run with a context
// cancelled by SIGINT/SIGTERM.
//
// run owns everything after that, including migrations (they must run before
// the shared pool opens), the graph through OpenCore and the service wiring.
func Main(service string, run func(context.Context, *config.Config, *slog.Logger) error) {
	cfg := config.Load()
	// An invalid level logs at info until Validate rejects it in Run.
	level, _ := cfg.SlogLevel()
	logger := obs.NewLogger(os.Stdout, level)
	// Libraries that log through the default logger get the same output.
	slog.SetDefault(logger)
	obs.CapMemory(logger)

	if err := Run(cfg, logger, run); err != nil {
		logger.ErrorContext(context.Background(), service, "err", err)
		os.Exit(1)
	}
}

// Run is the process lifecycle without the exit. It validates the
// configuration and calls run with a context cancelled by SIGINT/SIGTERM.
func Run(cfg *config.Config, logger *slog.Logger, run func(context.Context, *config.Config, *slog.Logger) error) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	// A Trino guard below 0.10.2 does not enforce baleia.catalogs (ADR 0018);
	// warn at startup, and let BALEIA_REQUIRE_CATALOG_SCOPE refuse instead.
	if msg := cfg.CatalogScopeWarning(); msg != "" {
		logger.ErrorContext(context.Background(), msg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(ctx, cfg, logger)
}

// OpenCore builds the dependency graph the API and the worker share and starts
// the runtime profiles listener. The caller defers Close on the returned Core.
func OpenCore(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts app.Options) (*app.Core, error) {
	core, err := app.New(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}
	go obs.ServePprof(ctx, logger, cfg.PprofAddr)
	return core, nil
}
