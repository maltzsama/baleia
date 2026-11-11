package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// EnsureAuditPartitions creates the daily partitions of the audit trail and
// the query history up to BALEIA_PARTITION_DAYS_AHEAD days ahead (ADR 0026); it
// never drops one (retention does). Idempotent.
func EnsureAuditPartitions(ctx context.Context) error {
	err := defaultActivities.store.EnsureAuditPartitions(ctx, defaultActivities.partitionDaysAhead())
	defaultActivities.passDone(schedules.AuditPartitions, err)
	return err
}

// ExportAuditEvents runs one pass of the audit export to OpenSearch (#50); a
// no-op when no sink is configured.
func ExportAuditEvents(ctx context.Context) (int, error) {
	if defaultActivities.auditExport == nil {
		return 0, nil
	}
	n, err := defaultActivities.auditExport.Run(ctx)
	defaultActivities.passDone(schedules.AuditExport, err)
	return n, err
}

// defaultPartitionDaysAhead is the horizon when no configuration is wired.
const defaultPartitionDaysAhead = 14

// partitionDaysAhead is the configured provisioning horizon in days.
func (a *Activities) partitionDaysAhead() int {
	if a.cfg == nil || a.cfg.PartitionDaysAhead < 1 {
		return defaultPartitionDaysAhead
	}
	return a.cfg.PartitionDaysAhead
}
