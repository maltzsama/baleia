package activities

import (
	"context"

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

// RetainAudit drops the audit trail's partitions that are past their hot
// window, gated on the cold-sink cursor and manifest.
func RetainAudit(ctx context.Context) (int, error) {
	if defaultActivities.auditRetain == nil {
		return 0, nil
	}
	n, err := defaultActivities.auditRetain.Run(ctx)
	defaultActivities.passDone(schedules.AuditRetention, err)
	return len(n), err
}

// RetainQueryHistory drops the query-history partitions that are past their hot
// window, executions and statements as a pair.
func RetainQueryHistory(ctx context.Context) (int, error) {
	if defaultActivities.queryHistoryRetain == nil {
		return 0, nil
	}
	var dropped int
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

// SetAuditOffload wires the audit S3 cold offload (ADR 0024).
func (a *Activities) SetAuditOffload(o *offload.Offloader) { a.auditOffload = o }

// SetAuditRetain wires the audit-trail retention.
func (a *Activities) SetAuditRetain(r *retention.Retain) { a.auditRetain = r }

// SetQueryHistoryRetain wires the query-history retention (executions and
// statements).
func (a *Activities) SetQueryHistoryRetain(r []*retention.Retain) { a.queryHistoryRetain = r }
