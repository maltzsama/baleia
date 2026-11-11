package repo

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
)

// OffloadQueryExecutions returns up to limit executions with (started_at, id)
// strictly after (afterAt, afterID) and started_at before before, ordered by
// (started_at, id). The started_at predicate prunes the daily partitions of
// query_executions (ADR 0024). afterAt's zero value means "from the first row".
func (r *Repo) OffloadQueryExecutions(ctx context.Context, afterAt time.Time, afterID string, before time.Time, limit int) ([]model.QueryExecution, error) {
	q := r.Users.db.WithContext(ctx).
		Where("started_at < ?", before).
		Order("started_at, id").
		Limit(limit)
	if !afterAt.IsZero() {
		if afterID != "" {
			// >= plus the tiebreaker keeps a row at exactly the cursor's
			// started_at reachable when its id is greater.
			q = q.Where("started_at >= ? AND (started_at > ? OR id > ?)", afterAt, afterAt, afterID)
		} else {
			q = q.Where("started_at >= ?", afterAt)
		}
	}
	var rows []model.QueryExecution
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// OffloadQueryStatements returns a page of executions' statements in position
// order, capped at limit. The second result reports truncation. The
// execution_started_at bounds pin the parent's day partition.
func (r *Repo) OffloadQueryStatements(ctx context.Context, executionIDs []model.UUID, from, until time.Time, limit int) ([]model.QueryStatement, bool, error) {
	if len(executionIDs) == 0 {
		return nil, false, nil
	}
	var rows []model.QueryStatement
	err := r.Users.db.WithContext(ctx).
		Where("execution_id IN ? AND execution_started_at >= ? AND execution_started_at < ?", executionIDs, from, until).
		Order("execution_id, position").
		Limit(limit + 1).
		Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	if len(rows) > limit {
		return rows[:limit], true, nil
	}
	return rows, false, nil
}

// PendingExecutions reports whether any execution in [from, until) is still
// queued or running, so its day cannot be sealed until it finishes.
func (r *Repo) PendingExecutions(ctx context.Context, from, until time.Time) (bool, error) {
	var n int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("started_at >= ? AND started_at < ? AND status IN ?", from, until, []string{"queued", "running"}).
		Count(&n).Error
	return n > 0, err
}

// DeleteStatementReferencesByPeriod removes a period's statement references so
// the period's query_statements partition can be detached (ADR 0024).
func (r *Repo) DeleteStatementReferencesByPeriod(ctx context.Context, from, until time.Time) error {
	return r.Users.db.WithContext(ctx).
		Where("execution_started_at >= ? AND execution_started_at < ?", from, until).
		Delete(&model.StatementReference{}).Error
}

// DeleteStatementJoinsByPeriod removes a period's statement equi-joins so the
// period's query_statements partition can be detached (ADR 0024).
func (r *Repo) DeleteStatementJoinsByPeriod(ctx context.Context, from, until time.Time) error {
	return r.Users.db.WithContext(ctx).
		Where("execution_started_at >= ? AND execution_started_at < ?", from, until).
		Delete(&model.StatementJoin{}).Error
}

// UnlinkTrinoQuerySnapshotsByPeriod detaches a period's Trino query snapshots from
// their executions (NULLing the link) so the period's query_executions partition
// can be detached without discarding the snapshots' metrics. The snapshots are
// monitoring data with their own lifecycle, not part of the cold offload.
func (r *Repo) UnlinkTrinoQuerySnapshotsByPeriod(ctx context.Context, from, until time.Time) error {
	return r.Users.db.WithContext(ctx).
		Model(&model.TrinoQuerySnapshot{}).
		Where("execution_started_at >= ? AND execution_started_at < ?", from, until).
		Updates(map[string]any{"execution_id": nil, "execution_started_at": nil}).Error
}
