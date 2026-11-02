package activities

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// MarkQueuedParams names the run that entered its engine's admission queue.
type MarkQueuedParams struct {
	ExecutionID string
}

// MarkQueued flips a running execution to queued (#399). It is conditional on
// `running`, so a concurrent failure or cancel is never overwritten.
func MarkQueued(ctx context.Context, params MarkQueuedParams) error {
	return defaultActivities.markQueued(ctx, params)
}

func (a *Activities) markQueued(ctx context.Context, params MarkQueuedParams) error {
	var id pgtype.UUID
	if err := id.Scan(params.ExecutionID); err != nil {
		return err
	}
	_, err := a.queries.QueueQueryExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// MarkAdmittedParams names the run that left its engine's admission queue.
type MarkAdmittedParams struct {
	ExecutionID string
}

// MarkAdmitted flips a queued execution back to running and records the wait.
func MarkAdmitted(ctx context.Context, params MarkAdmittedParams) error {
	return defaultActivities.markAdmitted(ctx, params)
}

func (a *Activities) markAdmitted(ctx context.Context, params MarkAdmittedParams) error {
	var id pgtype.UUID
	if err := id.Scan(params.ExecutionID); err != nil {
		return err
	}
	_, err := a.queries.AdmitQueryExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
