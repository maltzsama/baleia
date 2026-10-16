package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// auditMonthsAhead is how many months of audit partitions exist ahead of the
// current one (#45).
const auditMonthsAhead = 3

// EnsureAuditPartitions creates the audit trail's monthly partitions up to
// auditMonthsAhead months ahead; it never drops one (retention is the
// operator's `migrate -cmd=audit-prune`). Idempotent.
func EnsureAuditPartitions(ctx context.Context) error {
	err := defaultActivities.queries.EnsureAuditPartitions(ctx, auditMonthsAhead)
	defaultActivities.passDone(schedules.AuditPartitions, err)
	return err
}
