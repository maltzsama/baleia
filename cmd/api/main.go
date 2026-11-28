package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/maltzsama/baleia/internal/app"
	"github.com/maltzsama/baleia/internal/bootstrap"
	"github.com/maltzsama/baleia/internal/command"
	"github.com/maltzsama/baleia/internal/config"
	"github.com/maltzsama/baleia/internal/datapolicy"
	"github.com/maltzsama/baleia/internal/server"
	"github.com/maltzsama/baleia/internal/store"
	"github.com/maltzsama/baleia/internal/store/repo"
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

	state := bootstrap.APIState{
		Store: core.Store, Keyring: core.Keyring, TrinoCA: core.TrinoCA, Connections: core.Connections,
	}
	// Data policies (ADR 0028): the signing key is loaded only while they are on.
	if core.DataPolicySigningKey != nil && core.Storage != nil {
		state.DataPolicies = &datapolicy.Publisher{
			Store: repo.NewDataPolicies(core.Store), Objects: core.Storage, Key: core.DataPolicySigningKey,
			Lease: cfg.DataPolicyLease, IdentityMode: cfg.TrinoUserIdentity,
		}
	}
	if err := bootstrap.PrepareAPI(ctx, logger, cfg, state); err != nil {
		return err
	}

	srv, err := server.New(ctx, core)
	if err != nil {
		return fmt.Errorf("server setup: %w", err)
	}
	return srv.Run(ctx)
}
