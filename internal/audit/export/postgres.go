package export

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
)

// exportStore is the part of Baleia's database the export uses.
type exportStore interface {
	ListAuditEventsForExport(ctx context.Context, arg db.ListAuditEventsForExportParams) ([]db.ListAuditEventsForExportRow, error)
	GetAuditExportCursor(ctx context.Context, stream, sink string) (db.GetAuditExportCursorRow, error)
	SetAuditExportCursor(ctx context.Context, arg db.SetAuditExportCursorParams) error
}

// Postgres is the production Source and Cursor over Baleia's database. It
// reads audit_events and only ever touches audit_export_cursor.
type Postgres struct{ Queries exportStore }

// Events returns the batch after the position, before the safety-lag cutoff.
func (p Postgres) Events(ctx context.Context, after Position, before time.Time, limit int) ([]Event, error) {
	rows, err := p.Queries.ListAuditEventsForExport(ctx, db.ListAuditEventsForExportParams{
		AfterAt: timestamptz(after.OccurredAt), AfterID: after.ID, Before: timestamptz(before), Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{
			OccurredAt: r.OccurredAt.Time, ID: r.ID, ActorKind: r.ActorKind, ActorID: r.ActorID,
			AuthMethod: r.AuthMethod, Action: r.Action, ResourceType: r.ResourceType, ResourceID: r.ResourceID,
			WorkspaceID: r.WorkspaceID, Outcome: r.Outcome, Reason: r.Reason, RequestID: r.RequestID,
			TraceID: r.TraceID, ClientIP: r.ClientIp, UserAgent: r.UserAgent, Changes: r.Changes,
		})
	}
	return out, nil
}

// Get returns the stream and sink's position, or the zero position when none
// is stored.
func (p Postgres) Get(ctx context.Context, stream, sink string) (Position, error) {
	row, err := p.Queries.GetAuditExportCursor(ctx, stream, sink)
	if errors.Is(err, db.ErrNoRows) {
		return Position{}, nil
	}
	if err != nil {
		return Position{}, err
	}
	return Position{OccurredAt: row.OccurredAt.Time, ID: row.ID}, nil
}

// Set advances the stream and sink's cursor (upsert).
func (p Postgres) Set(ctx context.Context, stream, sink string, pos Position) error {
	return p.Queries.SetAuditExportCursor(ctx, db.SetAuditExportCursorParams{
		Stream: stream, Sink: sink, OccurredAt: timestamptz(pos.OccurredAt), ID: pos.ID,
	})
}

func timestamptz(t time.Time) db.Timestamptz { return db.Timestamptz{Time: t, Valid: true} }
