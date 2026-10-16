package export

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the production Source and Cursor over Baleia's pool. It reads
// audit_events and only ever touches audit_export_cursor.
type Postgres struct{ Pool *pgxpool.Pool }

const eventsSQL = `
SELECT occurred_at, id, actor_kind, coalesce(actor_id,''), coalesce(auth_method,''), action,
       resource_type, coalesce(resource_id,''), coalesce(workspace_id::text,''), outcome,
       coalesce(reason,''), coalesce(request_id,''), coalesce(trace_id,''),
       coalesce(host(client_ip),''), coalesce(user_agent,''), changes
FROM audit_events
WHERE (occurred_at, id) > ($1, $2) AND occurred_at < $3
ORDER BY occurred_at, id
LIMIT $4`

// Events returns the batch after the position, before the safety-lag cutoff.
func (p Postgres) Events(ctx context.Context, after Position, before time.Time, limit int) ([]Event, error) {
	rows, err := p.Pool.Query(ctx, eventsSQL, after.OccurredAt, after.ID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var changes []byte
		if err := rows.Scan(&e.OccurredAt, &e.ID, &e.ActorKind, &e.ActorID, &e.AuthMethod, &e.Action,
			&e.ResourceType, &e.ResourceID, &e.WorkspaceID, &e.Outcome, &e.Reason, &e.RequestID,
			&e.TraceID, &e.ClientIP, &e.UserAgent, &changes); err != nil {
			return nil, err
		}
		e.Changes = changes
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns the sink's position, or the zero position when none is stored.
func (p Postgres) Get(ctx context.Context, sink string) (Position, error) {
	var pos Position
	err := p.Pool.QueryRow(ctx, `SELECT occurred_at, id FROM audit_export_cursor WHERE sink = $1`, sink).
		Scan(&pos.OccurredAt, &pos.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Position{}, nil
	}
	return pos, err
}

// Set advances the sink's cursor (upsert).
func (p Postgres) Set(ctx context.Context, sink string, pos Position) error {
	_, err := p.Pool.Exec(ctx, `
INSERT INTO audit_export_cursor (sink, occurred_at, id, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (sink) DO UPDATE SET occurred_at = EXCLUDED.occurred_at, id = EXCLUDED.id, updated_at = now()`,
		sink, pos.OccurredAt, pos.ID)
	return err
}
