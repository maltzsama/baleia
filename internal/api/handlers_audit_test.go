package api

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestAuditList_Scoping: a member sees only the events they performed, even
// when asking for someone else's; an admin sees all, page by page.
func TestAuditList_Scoping(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	rec := audit.NewPostgres(q, nil, nil)
	const alice, bob = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	as := func(user string) context.Context {
		return context.WithValue(context.Background(), auth.ContextKeyUserID, user)
	}
	for _, u := range []string{alice, alice, bob} {
		if err := rec.Record(as(u), audit.Event{Action: audit.CatalogUpdate, ResourceType: "catalog", ResourceID: "c1", Outcome: audit.Allowed}); err != nil {
			t.Fatal(err)
		}
	}
	h := testHandlers(handlerFields{queries: q, tx: directTx{q: q}})
	list := func(ctx context.Context, p ListAuditEventsParams) ListAuditEvents200JSONResponse {
		t.Helper()
		r, err := h.ListAuditEvents(ctx, ListAuditEventsRequestObject{Params: p})
		if err != nil {
			t.Fatal(err)
		}
		return r.(ListAuditEvents200JSONResponse)
	}
	// The window is required unless actor_id or resource_id narrows it (#49).
	from := time.Now().Add(-time.Hour)
	member := ctxAs(alice, "member")
	if got := list(member, ListAuditEventsParams{From: &from}); len(got.Items) != 2 || *got.Items[0].ActorId != alice {
		t.Fatalf("member sees %+v", got.Items)
	}
	other := bob
	// The filter is ANDed with the caller's scope (#49), so nothing leaks.
	if got := list(member, ListAuditEventsParams{ActorId: &other}); len(got.Items) != 0 {
		t.Fatalf("a member cannot read someone else's events: %+v", got.Items)
	}
	admin := ctxAs("33333333-3333-3333-3333-333333333333", "admin")
	one := 1
	page := list(admin, ListAuditEventsParams{From: &from, Limit: &one})
	seen := len(page.Items)
	for page.NextCursor != nil {
		page = list(admin, ListAuditEventsParams{From: &from, Limit: &one, Cursor: page.NextCursor})
		seen += len(page.Items)
	}
	if seen != 3 {
		t.Fatalf("admin paged through %d events, want 3", seen)
	}
	if got := list(admin, ListAuditEventsParams{ActorId: &other}); len(got.Items) != 1 || *got.Items[0].ActorId != bob {
		t.Fatalf("admin filter by actor: %+v", got.Items)
	}
}

// endlessAudit returns a full page of events on every call.
type endlessAudit struct {
	db.Querier
	calls int
}

func (q *endlessAudit) ListAuditEvents(ctx context.Context, p db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error) {
	q.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return make([]db.ListAuditEventsRow, p.Lim), nil
}

// TestWriteAuditCSV_StopsWhenTheClientGoes: once the reader is closed or the
// request is cancelled, the export stops querying (#371).
func TestWriteAuditCSV_StopsWhenTheClientGoes(t *testing.T) {
	params := db.ListAuditEventsParams{Lim: 1000}

	q := &endlessAudit{}
	h := testHandlers(handlerFields{queries: q, tx: directTx{q: q}})
	reader, writer := io.Pipe()
	_ = reader.Close()
	if err := h.writeAuditCSV(context.Background(), writer, params); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed reader: err = %v, want io.ErrClosedPipe", err)
	}
	if q.calls != 1 {
		t.Fatalf("closed reader: %d pages queried, want 1", q.calls)
	}

	q = &endlessAudit{}
	h = testHandlers(handlerFields{queries: q, tx: directTx{q: q}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.writeAuditCSV(ctx, io.Discard, params); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: err = %v, want context.Canceled", err)
	}
	if q.calls != 0 {
		t.Fatalf("cancelled request: %d pages queried, want 0", q.calls)
	}
}
