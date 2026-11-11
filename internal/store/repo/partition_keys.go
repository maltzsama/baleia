package repo

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// The query-history tables are partitioned by month (ADR 0024), so every child
// row carries its parent's month in a denormalized column that is half of a
// composite foreign key. The value is always read from the parent row, never
// taken from the caller's clock: the column's precision differs per backend and
// the foreign key compares for exact equality.

// executionStartedAt returns started_at of the execution.
func executionStartedAt(ctx context.Context, db *gorm.DB, executionID string) (time.Time, error) {
	var started []time.Time
	err := db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("id = ?", executionID).Limit(1).Pluck("started_at", &started).Error
	if err != nil {
		return time.Time{}, err
	}
	if len(started) == 0 {
		return time.Time{}, gorm.ErrRecordNotFound
	}
	return started[0], nil
}

// statementExecutionStartedAt returns execution_started_at of the statement.
func statementExecutionStartedAt(ctx context.Context, db *gorm.DB, statementID string) (time.Time, error) {
	var started []time.Time
	err := db.WithContext(ctx).Model(&model.QueryStatement{}).
		Where("id = ?", statementID).Limit(1).Pluck("execution_started_at", &started).Error
	if err != nil {
		return time.Time{}, err
	}
	if len(started) == 0 {
		return time.Time{}, gorm.ErrRecordNotFound
	}
	return started[0], nil
}
