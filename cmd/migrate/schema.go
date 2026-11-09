package main

import (
	"context"
	"log/slog"

	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/store"
)

// runStoreUp applies the versioned schema migrations and the deploy-time
// baleia_trino role they used to own.
func runStoreUp(ctx context.Context, logger *slog.Logger, cfg *config.Config) error {
	db, err := openStore(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(db) }()

	if err := store.ApplyMigrations(ctx, db, logger); err != nil {
		return err
	}
	return bootstrap.EnsureTrinoRole(ctx, db)
}

func runStoreDown(ctx context.Context, logger *slog.Logger, cfg *config.Config) error {
	db, err := openStore(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(db) }()

	reg, err := store.AllMigrations()
	if err != nil {
		return err
	}
	m, err := store.NewRunner(db, reg, logger).Down(ctx)
	if err != nil {
		return err
	}
	if m == nil {
		logger.InfoContext(ctx, "no reversible store migration to roll back")
		return nil
	}
	logger.InfoContext(ctx, "store migration rolled back", "version", m.Version, "name", m.Name)
	return nil
}

func runStoreStatus(ctx context.Context, logger *slog.Logger, cfg *config.Config) error {
	db, err := openStore(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(db) }()

	reg, err := store.AllMigrations()
	if err != nil {
		return err
	}
	statuses, err := store.NewRunner(db, reg, logger).Status(ctx)
	if err != nil {
		return err
	}
	for _, st := range statuses {
		state := "pending"
		if st.Applied {
			state = "applied"
		}
		logger.InfoContext(ctx, "store migration", "version", st.Version, "name", st.Name, "state", state, "dirty", st.Dirty, "applied_at", st.AppliedAt)
	}
	return nil
}
