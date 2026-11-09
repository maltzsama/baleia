package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/store"
)

// pruneAudit drops the audit partitions of the months before before (YYYY-MM).
func pruneAudit(ctx context.Context, logger *slog.Logger, cfg *config.Config, before string) error {
	backend, err := store.BackendFromDSN(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL, backend, store.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(db) }()

	dropped, err := audit.Prune(ctx, db, before, time.Now().UTC())
	for _, name := range dropped {
		logger.InfoContext(ctx, "audit partition dropped", "partition", name)
	}
	return err
}
