package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/command"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/server"
	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/repo"
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
