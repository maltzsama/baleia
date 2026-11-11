package workerapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/audit"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/audit/offload"
	"github.com/galileostudio/baleia/internal/audit/retention"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/temporal/activities"
)

// wireColdRetention wires the S3 cold offload and the retention into the
// activities (ADR 0024, ADR 0026). Each stream has its own bucket. A stream with
// a bucket is offloaded and its retention is gated on the cold copy; a stream
// without one is left unwired and its retention stays fail-closed, unless it
// opts out explicitly (BALEIA_*_OFFLOAD_BEFORE_DROP=false), in which case its
// partitions are dropped on age alone, with a warning now and an audit event per
// drop.
func wireColdRetention(act *activities.Activities, core *app.Core, cfg *config.Config, logger *slog.Logger) error {
	q := core.Queries

	// Both streams get an allowance, wired or not: a stream that never drops
	// (no cold bucket, no opt-out) is exactly the one whose growth the
	// BaleiaRetentionBacklog alert has to see.
	if err := setAllowance(act, "audit", cfg.AuditRetentionWindow(), cfg.AuditGrace(), cfg.AuditRetentionCadence); err != nil {
		return err
	}
	if err := setAllowance(act, "query_history", cfg.QueryHistoryRetentionWindow(), cfg.QueryHistoryGrace(), cfg.QueryHistoryRetentionCadence); err != nil {
		return err
	}

	if cfg.AuditColdBucket != "" || cfg.AuditDropWithoutOffload {
		retain := &retention.Retain{
			Table: "audit_events", Stream: auditexport.StreamAudit, SinkName: auditexport.SinkS3,
			Partitions:         repoPartitions{q},
			Retention:          cfg.AuditRetentionWindow(),
			Grace:              cfg.AuditGrace(),
			MaxPartitions:      cfg.PruneCap(),
			DropWithoutOffload: cfg.AuditDropWithoutOffload,
		}
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
			retain.Cursor, retain.Manifest = repoCursor{q}, sink
		}
		if cfg.AuditDropWithoutOffload {
			warnDropWithoutOffload(logger, "audit", "BALEIA_AUDIT_OFFLOAD_BEFORE_DROP")
			retain.OnDropped = droppedWithoutOffload(core.Audit, auditexport.StreamAudit)
		}
		act.SetAuditRetain(retain)
	}

	if cfg.QueryHistoryColdBucket != "" || cfg.QueryHistoryDropWithoutOffload {
		var (
			sink   *offload.Sink
			cursor retention.Cursor
		)
		if cfg.QueryHistoryColdBucket != "" {
			qhCold, err := coldClient(cfg, cfg.QueryHistoryColdBucket)
			if err != nil {
				return err
			}
			sink = offload.NewSink(qhCold, cfg.QueryHistoryColdPrefix, auditexport.StreamQueryHistory)
			cursor = repoCursor{q}
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
		}
		var onDropped func(context.Context, partitioning.Partition)
		if cfg.QueryHistoryDropWithoutOffload {
			warnDropWithoutOffload(logger, "query_history", "BALEIA_QUERY_HISTORY_OFFLOAD_BEFORE_DROP")
			onDropped = droppedWithoutOffload(core.Audit, auditexport.StreamQueryHistory)
		}
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
			r := &retention.Retain{
				Table: table, Stream: auditexport.StreamQueryHistory, SinkName: auditexport.SinkS3,
				Partitions:         repoPartitions{q},
				Retention:          cfg.QueryHistoryRetentionWindow(),
				Grace:              cfg.QueryHistoryGrace(),
				MaxPartitions:      cfg.PruneCap(),
				PreDrop:            preDrop,
				DropWithoutOffload: cfg.QueryHistoryDropWithoutOffload,
				OnDropped:          onDropped,
			}
			if sink != nil {
				r.Cursor, r.Manifest = cursor, sink
			}
			retains = append(retains, r)
		}
		act.SetQueryHistoryRetain(retains)
	}
	return nil
}

// retentionAllowance is how old a stream's oldest partition may get before
// retention is behind: the window, the grace, the longest gap between two runs
// of the cadence, and the day the partition itself spans (ADR 0026). A day that
// has just become droppable can wait one full cadence for the run that drops it.
func retentionAllowance(window, grace time.Duration, cadence string) (time.Duration, error) {
	period, err := config.RetentionPeriod(cadence)
	if err != nil {
		return 0, err
	}
	return window + grace + period + 24*time.Hour, nil
}

func setAllowance(act *activities.Activities, stream string, window, grace time.Duration, cadence string) error {
	allowed, err := retentionAllowance(window, grace, cadence)
	if err != nil {
		return fmt.Errorf("%s retention cadence: %w", stream, err)
	}
	act.SetRetentionAllowance(stream, allowed)
	return nil
}

// warnDropWithoutOffload says, at startup, that a stream will lose data it has
// no copy of: the opt-out exists for deployments that keep none on purpose.
func warnDropWithoutOffload(logger *slog.Logger, stream, setting string) {
	if logger == nil {
		return
	}
	logger.Warn("retention drops partitions without a cold copy",
		"stream", stream, "setting", setting+"=false",
		"effect", "partitions past the retention window are dropped on age alone and cannot be recovered")
}

// droppedWithoutOffload records an audit event for a partition dropped with no
// offload behind it, naming the stream and the day. It is best-effort, like every
// audit write.
func droppedWithoutOffload(rec audit.Recorder, stream string) func(context.Context, partitioning.Partition) {
	return func(ctx context.Context, p partitioning.Partition) {
		if rec == nil {
			return
		}
		_ = rec.Record(audit.WithService(ctx, "retention"), audit.Event{
			Action: audit.PartitionDrop, ResourceType: "partition", ResourceID: p.Name,
			Outcome: audit.Allowed, Reason: audit.ReasonNoOffload,
			Changes: map[string]audit.Change{
				"stream": {To: stream},
				"day":    {To: p.From.UTC().Format("2006-01-02")},
			},
		})
	}
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
