package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/command"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/galileostudio/baleia/internal/store"
)

func main() {
	command.Main("api", run)
}

// run returns instead of exiting so every deferred Close runs.
func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	// Migrations run on their own connection, before the pool opens.
	if err := store.MigrateUp(ctx, cfg.DatabaseURL, logger); err != nil {
		return fmt.Errorf("migration: %w", err)
	}

	core, err := command.OpenCore(ctx, cfg, logger, app.Options{Service: "api", PoolSize: 20, Logger: logger})
	if err != nil {
		return err
	}
	defer core.Close()

	if err := bootstrap.PrepareAPI(ctx, logger, cfg, bootstrap.APIState{
		Store: core.Store, Keyring: core.Keyring, TrinoCA: core.TrinoCA, Connections: core.Connections,
	}); err != nil {
		return err
	}

	srv, err := server.New(ctx, core)
	if err != nil {
		return fmt.Errorf("server setup: %w", err)
	}
	return srv.Run(ctx)
}
