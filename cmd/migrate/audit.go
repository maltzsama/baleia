package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/audit/offload"
	"github.com/galileostudio/baleia/internal/audit/retention"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// pruneAudit drops the audit partitions before before (YYYY-MM-DD, or YYYY-MM
// for the first day of a month), applying the same cold-sink gates as the
// scheduled retention: a partition is dropped only when the audit cursor has
// advanced past it and its manifest exists and validates. It refuses to run
// without a configured cold bucket (fail-closed); with --dry-run it reports what
// it would drop without dropping anything.
func pruneAudit(ctx context.Context, logger *slog.Logger, cfg *config.Config, before string, dryRun bool) error {
	cut, err := parseBefore(before)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !cut.Before(partitioning.Daily.Start(now)) {
		return fmt.Errorf("refusing to prune today or later (before=%s)", before)
	}
	if cfg.AuditColdBucket == "" {
		return fmt.Errorf("refusing to prune without BALEIA_AUDIT_COLD_BUCKET: retention is fail-closed")
	}
	cold, err := storage.New(storage.Options{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		Bucket: cfg.AuditColdBucket, Region: cfg.S3Region, UseSSL: cfg.S3UseSSL,
	})
	if err != nil {
		return fmt.Errorf("cold store %s: %w", cfg.AuditColdBucket, err)
	}
	backend, err := store.BackendFromDSN(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	gdb, err := store.Open(ctx, cfg.DatabaseURL, backend, store.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close(gdb) }()

	q := repo.New(gdb)
	sink := offload.NewSink(cold, cfg.AuditColdPrefix, export.StreamAudit)
	r := &retention.Retain{
		Table: "audit_events", Stream: export.StreamAudit, SinkName: export.SinkS3,
		Partitions: prunePartitions{q: q},
		Cursor:     pruneCursor{q: q},
		Manifest:   sink,
		Cutoff:     cut,
		// The manual command drains every eligible partition before -before.
		MaxPartitions: -1,
		DryRun:        dryRun,
		OnSkip: func(partition, reason string) {
			logger.WarnContext(ctx, "audit partition skipped", "partition", partition, "reason", reason)
		},
	}
	names, err := r.Run(ctx)
	for _, name := range names {
		verb := "audit partition dropped"
		if dryRun {
			verb = "audit partition would drop"
		}
		logger.InfoContext(ctx, verb, "partition", name)
	}
	return err
}

// prunePartitions adapts the store to the retention Partitions contract.
type prunePartitions struct{ q *repo.Repo }

func (p prunePartitions) ListPartitions(ctx context.Context, table string) ([]partitioning.Partition, error) {
	return p.q.Partitions(ctx, table)
}

func (p prunePartitions) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	return p.q.DropPartitions(ctx, table, from, until)
}

// pruneCursor adapts the store to the retention Cursor contract.
type pruneCursor struct{ q *repo.Repo }

func (c pruneCursor) OccurredAt(ctx context.Context, stream, sink string) (time.Time, error) {
	row, err := c.q.GetAuditExportCursor(ctx, stream, sink)
	if errors.Is(err, db.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return row.OccurredAt.Time, nil
}

// parseBefore reads -before as a day (2026-01-15) or, for convenience, a month
// (2026-01, its first day).
func parseBefore(before string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if t, err := time.Parse(layout, before); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("-before must look like 2026-01-15 or 2026-01 (got %q)", before)
}
