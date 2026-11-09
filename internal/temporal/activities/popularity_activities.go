package activities

import (
	"context"

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
	refresher, ok := a.store.(interface {
		RefreshTablePopularity(context.Context) error
	})
	if !ok {
		return nil
	}
	if err := refresher.RefreshTablePopularity(ctx); err != nil {
		return err
	}
	activity.GetLogger(ctx).Info("popular tables refreshed")
	return nil
}
