package export

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestPostgres_EventsAndCursor: events after the cursor come back in order
// with empty strings for NULLs, and the cursor round-trips.
func TestPostgres_EventsAndCursor(t *testing.T) {
	t.Parallel()
	q := db.New(testdb.New(t))
	ctx := context.Background()
	rec := audit.NewPostgres(q, nil, nil)
	for range 2 {
		if err := rec.Record(ctx, audit.Event{Action: audit.CatalogUpdate, ResourceType: "catalog", Outcome: audit.Allowed}); err != nil {
			t.Fatal(err)
		}
	}
	p := Postgres{Queries: q}

	events, err := p.Events(ctx, Position{}, time.Now().Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID >= events[1].ID || events[0].ResourceID != "" {
		t.Fatalf("events = %+v", events)
	}

	if got, err := p.Get(ctx, "test"); err != nil || got.After() {
		t.Fatalf("unset cursor = %+v, %v; want the zero position", got, err)
	}
	pos := Position{OccurredAt: events[0].OccurredAt, ID: events[0].ID}
	if err := p.Set(ctx, "test", pos); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(ctx, "test")
	if err != nil || got.ID != pos.ID || !got.OccurredAt.Equal(pos.OccurredAt) {
		t.Fatalf("cursor = %+v, %v; want %+v", got, err, pos)
	}
	rest, err := p.Events(ctx, got, time.Now().Add(time.Minute), 10)
	if err != nil || len(rest) != 1 || rest[0].ID != events[1].ID {
		t.Fatalf("events after the cursor = %+v, %v", rest, err)
	}
}
