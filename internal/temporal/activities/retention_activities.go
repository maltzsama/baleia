package activities

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/audit/offload"
	"github.com/galileostudio/baleia/internal/audit/retention"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// OffloadAudit runs one pass of the audit S3 cold offload; a no-op when no
// cold sink is configured.
func OffloadAudit(ctx context.Context) (int, error) {
	if defaultActivities.auditOffload == nil {
		return 0, nil
	}
	n, err := defaultActivities.auditOffload.Run(ctx)
	defaultActivities.passDone(schedules.AuditOffload, err)
	return n, err
}

// OffloadQueryHistory runs one pass of the query-history S3 cold offload; a
// no-op when no cold sink is configured.
func OffloadQueryHistory(ctx context.Context) (int, error) {
	if defaultActivities.queryHistoryOffload == nil {
		return 0, nil
	}
	n, err := defaultActivities.queryHistoryOffload.Run(ctx)
	defaultActivities.passDone(schedules.QueryHistoryOffload, err)
	return n, err
}

// RetainAudit drops the audit trail's partitions that are past their hot
// window, gated on the cold-sink cursor and manifest.
func RetainAudit(ctx context.Context) (int, error) {
	if defaultActivities.auditRetain == nil {
		return 0, nil
	}
	n, err := defaultActivities.auditRetain.Run(ctx)
	defaultActivities.passDone(schedules.AuditRetention, err)
	defaultActivities.observeAllRetention(ctx)
	return len(n), err
}

// RetainQueryHistory drops the query-history partitions that are past their hot
// window, executions and statements as a pair.
func RetainQueryHistory(ctx context.Context) (int, error) {
	if defaultActivities.queryHistoryRetain == nil {
		return 0, nil
	}
	var dropped int
	defer defaultActivities.observeAllRetention(ctx)
	for _, r := range defaultActivities.queryHistoryRetain {
		n, err := r.Run(ctx)
		if err != nil {
			defaultActivities.passDone(schedules.QueryHistoryRetention, err)
			return dropped, err
		}
		dropped += len(n)
	}
	defaultActivities.passDone(schedules.QueryHistoryRetention, nil)
	return dropped, nil
}

// SetRetentionAllowance sets how old a stream's oldest partition may get before
// retention is behind: retention, grace, the cadence and one day (ADR 0026).
func (a *Activities) SetRetentionAllowance(stream string, d time.Duration) {
	if a.retentionAllowance == nil {
		a.retentionAllowance = map[string]time.Duration{}
	}
	a.retentionAllowance[stream] = d
}

// observeRetention exports the start of a stream's oldest partition, read
// from the store so it is exported whether or not the stream's retention is
// wired: a stream with no cold bucket and no opt-out never drops, and that
// growth is what the alert is for. It never fails a pass: a gauge that cannot be
// read is logged, and the alert on the last value still stands.
func (a *Activities) observeRetention(ctx context.Context, stream, table string) {
	if a == nil || a.store == nil {
		return
	}
	allowed, ok := a.retentionAllowance[stream]
	if !ok {
		return
	}
	parts, err := a.store.Partitions(ctx, table)
	if err != nil {
		if a.logger != nil {
			a.logger.WarnContext(ctx, "retention gauge: list partitions", "stream", stream, "err", err)
		}
		return
	}
	if len(parts) == 0 {
		return
	}
	oldest := parts[0].From
	for _, p := range parts[1:] {
		if p.From.Before(oldest) {
			oldest = p.From
		}
	}
	a.metrics.retention.Observe(stream, oldest, allowed)
}

// observeAllRetention refreshes both gauges: after each retention pass, and in
// the daily audit-partitions pass, so a weekly cadence does not leave them a
// week old. The query history is read through its executions table, the parent.
func (a *Activities) observeAllRetention(ctx context.Context) {
	a.observeRetention(ctx, "audit", "audit_events")
	a.observeRetention(ctx, "query_history", "query_executions")
}

// SetAuditOffload wires the audit S3 cold offload (ADR 0024).
func (a *Activities) SetAuditOffload(o *offload.Offloader) { a.auditOffload = o }

// SetQueryHistoryOffload wires the query-history S3 cold offload.
func (a *Activities) SetQueryHistoryOffload(o *offload.Offloader) { a.queryHistoryOffload = o }

// SetAuditRetain wires the audit-trail retention.
func (a *Activities) SetAuditRetain(r *retention.Retain) { a.auditRetain = r }

// SetQueryHistoryRetain wires the query-history retention (executions and
// statements).
func (a *Activities) SetQueryHistoryRetain(r []*retention.Retain) { a.queryHistoryRetain = r }
