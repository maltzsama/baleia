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

// pruneAudit drops the audit partitions of the months before before (YYYY-MM),
// applying the same cold-sink gates as the scheduled retention: a partition is
// dropped only when the audit cursor has advanced past it and its manifest
// exists. It refuses to run without a configured cold bucket (fail-closed); with
// --dry-run it reports what it would drop without dropping anything.
func pruneAudit(ctx context.Context, logger *slog.Logger, cfg *config.Config, before string, dryRun bool) error {
	cut, err := time.Parse("2006-01", before)
	if err != nil {
		return fmt.Errorf("-before must look like 2026-01: %w", err)
	}
	now := time.Now().UTC()
	if !cut.Before(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		return fmt.Errorf("refusing to prune the current month or later (before=%s)", before)
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
		Manifest:   pruneManifest{sink: sink, has: cold.Has},
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
	return p.q.MonthPartitions(ctx, table)
}

func (p prunePartitions) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	return p.q.DropMonth(ctx, table, from, until)
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

// pruneManifest reads the cold store for a month's manifest.
type pruneManifest struct {
	sink *offload.Sink
	has  func(ctx context.Context, key string) (bool, error)
}

func (m pruneManifest) Exists(ctx context.Context, _ string, month time.Time) (bool, error) {
	return m.has(ctx, m.sink.ManifestKey(month))
}
