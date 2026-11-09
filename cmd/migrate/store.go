package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/store"
	"gorm.io/gorm"
)

// openStore opens Baleia's own store through GORM (ADR 0020). The dialector is
// selected by the configured backend, never by the DSN alone.
func openStore(ctx context.Context, logger *slog.Logger, cfg *config.Config) (*gorm.DB, error) {
	backend, err := store.BackendFromDSN(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, cfg.DatabaseURL, backend, store.Options{
		MaxOpenConns: int(cfg.PoolSize(4)),
		Logger:       logger,
	})
}

// openSQLDB opens the GORM store and hands back its database/sql handle for
// the commands that backfill rows with explicit transactions. The driver is
// whatever GORM's dialector uses; this command imports no driver directly.
func openSQLDB(ctx context.Context, logger *slog.Logger, cfg *config.Config) (*sql.DB, func() error, error) {
	gdb, err := openStore(ctx, logger, cfg)
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		_ = store.Close(gdb)
		return nil, nil, err
	}
	return sqlDB, func() error { return store.Close(gdb) }, nil
}
