package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"go.temporal.io/sdk/activity"
)

// RefreshTablePopularity recomputes the popular tables of every workspace
// (B7, #562). The delete and insert share one transaction, so a reader sees
// the previous snapshot or the new one, never an empty table.
func RefreshTablePopularity(ctx context.Context, _ struct{}) error {
	err := defaultActivities.refreshTablePopularity(ctx)
	defaultActivities.passDone(schedules.TablePopularity, err)
	return err
}

func (a *Activities) refreshTablePopularity(ctx context.Context) error {
	if a.store != nil {
		if err := a.store.RefreshTablePopularity(ctx); err != nil {
			return err
		}
		activity.GetLogger(ctx).Info("popular tables refreshed")
		return nil
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := db.New(tx)
	if err := q.DeleteTablePopularity(ctx); err != nil {
		return err
	}
	if err := q.InsertTablePopularity(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	activity.GetLogger(ctx).Info("popular tables refreshed")
	return nil
}
