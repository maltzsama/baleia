package api

import (
	"context"
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
	h := &Handlers{queries: q}
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
