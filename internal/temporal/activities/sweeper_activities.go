package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"go.temporal.io/sdk/activity"
)

// ephemeralSweeper drops expired connection-test catalogs and returns how
// many (the Trino binder's SweepEphemeral); wired in cmd/worker.
type ephemeralSweeper func(ctx context.Context) (int, error)

// SetEphemeralSweeper wires the sweeper of connection-test catalogs (#12).
func (a *Activities) SetEphemeralSweeper(s ephemeralSweeper) { a.sweepEphemeral = s }

// SweepEphemeralCatalogs drops the short-lived catalogs connection tests
// left behind (a crashed API, a lost drop): their registry rows hold real
// credentials and expire after two minutes.
func SweepEphemeralCatalogs(ctx context.Context) (int, error) {
	a := defaultActivities
	if a.sweepEphemeral == nil {
		return 0, nil
	}
	n, err := a.sweepEphemeral(ctx)
	if n > 0 {
		activity.GetLogger(ctx).Info("swept expired test catalogs", "count", n)
	}
	a.passDone(schedules.EphemeralCatalogSweeper, err)
	return n, err
}
