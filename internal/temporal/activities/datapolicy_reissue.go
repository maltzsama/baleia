package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// SetDataPolicyPublisher wires the data policy publisher (ADR 0028); nil
// while data policies are off.
func (a *Activities) SetDataPolicyPublisher(p *datapolicy.Publisher) { a.dataPolicies = p }

// ReissueDataPolicies keeps the published data policy revision current: a
// new revision when the subjects changed (team membership, sign-in groups),
// or a fresh lease once less than half of it is left. The schedule's cadence
// is the debounce: changes made between two passes land in one revision.
func ReissueDataPolicies(ctx context.Context) error {
	a := defaultActivities
	if a.dataPolicies == nil {
		a.passDone(schedules.DataPolicyReissue, nil)
		return nil
	}
	rev, changed, err := a.dataPolicies.Reissue(ctx)
	if err == nil && changed {
		a.logger.InfoContext(ctx, "data policy revision re-issued", "revision", rev.ID, "number", rev.Number, "reason", rev.Reason)
	}
	a.passDone(schedules.DataPolicyReissue, err)
	return err
}
