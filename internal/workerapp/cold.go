package workerapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/app"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/audit/offload"
	"github.com/galileostudio/baleia/internal/audit/retention"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/temporal/activities"
)

// wireColdRetention wires the S3 cold offload and the gated retention into the
// activities (ADR 0024). Each stream has its own bucket; a stream whose bucket
// is empty is left unwired, and its retention stays fail-closed.
func wireColdRetention(act *activities.Activities, core *app.Core, cfg *config.Config) error {
	if cfg.AuditColdBucket == "" && cfg.QueryHistoryColdBucket == "" {
		return nil
	}
	q := core.Queries

	if cfg.AuditColdBucket != "" {
		cold, err := coldClient(cfg, cfg.AuditColdBucket)
		if err != nil {
			return err
		}
		sink := offload.NewSink(cold, cfg.AuditColdPrefix, auditexport.StreamAudit)
		pg := auditexport.Postgres{Queries: q}
		act.SetAuditOffload(&offload.Offloader{
			Stream: auditexport.StreamAudit, SinkName: auditexport.SinkS3,
			Source: pg, Cursor: pg, Sink: sink,
		})
		act.SetAuditRetain(&retention.Retain{
			Table: "audit_events", Stream: auditexport.StreamAudit, SinkName: auditexport.SinkS3,
			Partitions: repoPartitions{q}, Cursor: repoCursor{q},
			Manifest:      &manifestCheck{sink: sink, has: cold.Has},
			Retention:     cfg.AuditRetention,
			Grace:         cfg.AuditColdGrace,
			MaxPartitions: cfg.PruneMaxPartitions,
		})
	}

	if cfg.QueryHistoryColdBucket != "" {
		qhCold, err := coldClient(cfg, cfg.QueryHistoryColdBucket)
		if err != nil {
			return err
		}
		sink := offload.NewSink(qhCold, cfg.QueryHistoryColdPrefix, auditexport.StreamQueryHistory)
		var retains []*retention.Retain
		for _, table := range []string{"query_executions", "query_statements"} {
			retains = append(retains, &retention.Retain{
				Table: table, Stream: auditexport.StreamQueryHistory, SinkName: auditexport.SinkS3,
				Partitions: repoPartitions{q}, Cursor: repoCursor{q},
				Manifest:      &manifestCheck{sink: sink, has: qhCold.Has},
				Retention:     cfg.QueryHistoryRetention,
				Grace:         cfg.QueryHistoryColdGrace,
				MaxPartitions: cfg.PruneMaxPartitions,
			})
		}
		act.SetQueryHistoryRetain(retains)
	}
	return nil
}

func coldClient(cfg *config.Config, bucket string) (*storage.S3Client, error) {
	c, err := storage.New(storage.Options{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		Bucket: bucket, Region: cfg.S3Region, UseSSL: cfg.S3UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("cold store %s: %w", bucket, err)
	}
	return c, nil
}

// repoPartitions adapts the store to the retention Partitions contract.
type repoPartitions struct{ q db.Querier }

func (p repoPartitions) ListPartitions(ctx context.Context, table string) ([]partitioning.Partition, error) {
	return p.q.MonthPartitions(ctx, table)
}

func (p repoPartitions) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	return p.q.DropMonth(ctx, table, from, until)
}

// repoCursor adapts the store to the retention Cursor contract.
type repoCursor struct{ q db.Querier }

func (c repoCursor) OccurredAt(ctx context.Context, stream, sink string) (time.Time, error) {
	row, err := c.q.GetAuditExportCursor(ctx, stream, sink)
	if errors.Is(err, db.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return row.OccurredAt.Time, nil
}

// manifestCheck reads the cold store for a month's manifest.
type manifestCheck struct {
	sink *offload.Sink
	has  func(ctx context.Context, key string) (bool, error)
}

func (m *manifestCheck) Exists(ctx context.Context, _ string, month time.Time) (bool, error) {
	return m.has(ctx, m.sink.ManifestKey(month))
}
