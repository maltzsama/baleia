package store

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
)

// ApplyMigrations applies the store's versioned schema migrations on db and
// logs how many ran.
func ApplyMigrations(ctx context.Context, db *gorm.DB, logger *slog.Logger) error {
	registry, err := AllMigrations()
	if err != nil {
		return err
	}
	applied, err := NewRunner(db, registry, logger).Up(ctx)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "store migrations applied", "applied", len(applied))
	return nil
}

// MigrateUp applies the store's schema migrations on a dedicated connection.
// A process calls it before opening its own pool (the API does); the store-up
// command calls ApplyMigrations on the connection it already opened.
func MigrateUp(ctx context.Context, dsn string, logger *slog.Logger) error {
	backend, err := BackendFromDSN(dsn)
	if err != nil {
		return err
	}
	db, err := Open(ctx, dsn, backend, Options{Logger: logger})
	if err != nil {
		return err
	}
	defer func() { _ = Close(db) }()
	return ApplyMigrations(ctx, db, logger)
}
