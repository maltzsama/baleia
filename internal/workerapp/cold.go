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
			Cursor: pg, Sink: sink,
			Reader: &offload.AuditReader{Source: pg, Name: auditexport.StreamAudit},
		})
		act.SetAuditRetain(&retention.Retain{
			Table: "audit_events", Stream: auditexport.StreamAudit, SinkName: auditexport.SinkS3,
			Partitions: repoPartitions{q}, Cursor: repoCursor{q},
			Manifest:      sink,
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
		pg := auditexport.Postgres{Queries: q}
		act.SetQueryHistoryOffload(&offload.Offloader{
			Stream: auditexport.StreamQueryHistory, SinkName: auditexport.SinkS3,
			Cursor: pg, Sink: sink,
			// A day is only sealed a day after it ends, so an execution that
			// crosses the boundary is archived in its final state.
			Lag: offload.QueryHistorySafetyLag,
			// Executions and their statements are read together and written as
			// one day's pair (ADR 0024, ADR 0026).
			Reader: &offload.QueryHistoryReader{Store: core.StoreRepo},
		})
		var retains []*retention.Retain
		// Children first: query_statements references query_executions, so its
		// day partition must be detached and dropped before the parent's.
		// Each table's PreDrop removes the rows that still point into the day's
		// partition (references and joins for statements, snapshots for
		// executions), otherwise the detach fails and retention never prunes.
		storeRepo := core.StoreRepo
		for _, table := range []string{"query_statements", "query_executions"} {
			preDrop := func(ctx context.Context, from, until time.Time) error {
				return storeRepo.UnlinkTrinoQuerySnapshotsByPeriod(ctx, from, until)
			}
			if table == "query_statements" {
				preDrop = func(ctx context.Context, from, until time.Time) error {
					if err := storeRepo.DeleteStatementReferencesByPeriod(ctx, from, until); err != nil {
						return err
					}
					return storeRepo.DeleteStatementJoinsByPeriod(ctx, from, until)
				}
			}
			retains = append(retains, &retention.Retain{
				Table: table, Stream: auditexport.StreamQueryHistory, SinkName: auditexport.SinkS3,
				Partitions: repoPartitions{q}, Cursor: repoCursor{q},
				Manifest:      sink,
				Retention:     cfg.QueryHistoryRetention,
				Grace:         cfg.QueryHistoryColdGrace,
				MaxPartitions: cfg.PruneMaxPartitions,
				PreDrop:       preDrop,
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
	return p.q.Partitions(ctx, table)
}

func (p repoPartitions) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	return p.q.DropPartitions(ctx, table, from, until)
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
