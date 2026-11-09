package activities

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// AgentSlotParams asks whether an agent may start another run.
type AgentSlotParams struct {
	AgentID string
	Limit   int
}

// AgentSlotAvailable is the cross-engine per-agent limit (#589). Per-engine
// admission queues cannot see each other, so the execution workflow checks the
// active (running or queued) count before asking an engine for a slot.
func AgentSlotAvailable(ctx context.Context, p AgentSlotParams) (bool, error) {
	return defaultActivities.agentSlotAvailable(ctx, p)
}

func (a *Activities) agentSlotAvailable(ctx context.Context, p AgentSlotParams) (bool, error) {
	var id pgtype.UUID
	if err := id.Scan(p.AgentID); err != nil || !id.Valid || p.Limit <= 0 {
		return true, nil
	}
	n, err := a.store.CountActiveExecutionsByAgent(ctx, id)
	if err != nil {
		return false, err
	}
	return n <= int64(p.Limit), nil
}
