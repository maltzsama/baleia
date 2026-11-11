package repo

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
)

// OffloadQueryExecutions returns up to limit executions with (started_at, id)
// strictly after (afterAt, afterID) and started_at before before, ordered by
// (started_at, id). The started_at predicate prunes the monthly partitions of
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

// OffloadQueryStatements returns one execution's statements in position order.
// execution_started_at pins the parent's month partition.
func (r *Repo) OffloadQueryStatements(ctx context.Context, executionID model.UUID, executionStartedAt time.Time) ([]model.QueryStatement, error) {
	var rows []model.QueryStatement
	err := r.Users.db.WithContext(ctx).
		Where("execution_id = ? AND execution_started_at = ?", executionID, executionStartedAt).
		Order("position").
		Find(&rows).Error
	return rows, err
}
