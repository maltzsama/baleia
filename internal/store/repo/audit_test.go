package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestAuditInsertList covers the GORM replacement for audit.sql (#774): the
// append, the filters, the non-admin scope, keyset paging and the export
// cursor.
func TestAuditInsertList(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	r := f.r
	outsider := createAPIKeyUser(t, f.gdb, "audit-outsider@example.test")

	insert := func(actor, resource string, ws pgtype.UUID) {
		t.Helper()
		if err := r.InsertAuditEvent(ctx, db.InsertAuditEventParams{
			ActorKind: "user", ActorID: &actor, Action: "query.run", ResourceType: resource,
			Outcome: "allowed", WorkspaceID: ws,
		}); err != nil {
			t.Fatalf("InsertAuditEvent: %v", err)
		}
	}
	owner := "user:" + f.ownerID().String()
	member := "user:" + f.memberID().String()
	insert(owner, "query", f.wsID())
	insert(member, "saved_query", f.wsID())
	insert(owner, "catalog", f.wsID())

	// Admin reads everything, newest first.
	admin, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{Lim: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(admin) != 3 {
		t.Fatalf("events = %d, want 3", len(admin))
	}
	if admin[0].ID < admin[1].ID || admin[1].ID < admin[2].ID {
		t.Fatalf("events are not newest first: %+v", admin)
	}
	if admin[0].WorkspaceID != f.wsID() {
		t.Errorf("workspace = %v, want %v", admin[0].WorkspaceID, f.wsID())
	}

	// Filters narrow by resource type.
	queries, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{ResourceType: strPtr("query"), Lim: 10})
	if err != nil || len(queries) != 1 || queries[0].ResourceType != "query" {
		t.Fatalf("resource filter = %+v, %v", queries, err)
	}

	// A non-admin sees their own events, not the owner's: the workspace is
	// not one they own.
	memberView, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{
		ScopeActor: &member, ScopeOwner: f.memberID(), Lim: 10,
	})
	if err != nil || len(memberView) != 1 || derefString(memberView[0].ActorID) != member {
		t.Fatalf("member scope = %+v, %v", memberView, err)
	}
	// The owner owns the workspace, so the workspace's events are visible.
	ownerView, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{
		ScopeActor: &owner, ScopeOwner: f.ownerID(), Lim: 10,
	})
	if err != nil || len(ownerView) != 3 {
		t.Fatalf("owner scope = %d events, %v", len(ownerView), err)
	}

	// Keyset paging: the last row of a page is the next page's cursor.
	first, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{Lim: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d, %v", len(first), err)
	}
	last := first[len(first)-1]
	second, err := r.ListAuditEvents(ctx, db.ListAuditEventsParams{
		Lim: 2, CursorAt: last.OccurredAt, CursorID: &last.ID,
	})
	if err != nil || len(second) != 1 || second[0].ID >= last.ID {
		t.Fatalf("second page = %+v, %v", second, err)
	}

	// SharesWorkspace: both fixture users share the team workspace; an
	// outsider does not.
	shares, err := r.SharesWorkspace(ctx, db.SharesWorkspaceParams{CallerID: f.memberID(), UserID: f.ownerID()})
	if err != nil || !shares {
		t.Fatalf("SharesWorkspace(member, owner) = %v, %v", shares, err)
	}
	shares, err = r.SharesWorkspace(ctx, db.SharesWorkspaceParams{
		CallerID: f.memberID(), UserID: PGUUID(string(outsider.ID)),
	})
	if err != nil || shares {
		t.Fatalf("SharesWorkspace(member, outsider) = %v, %v", shares, err)
	}
}

// TestAuditExportCursor covers the export read and the cursor upsert (#774).
func TestAuditExportCursor(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	r := f.r
	actor := "service:exporter"

	if _, err := r.GetAuditExportCursor(ctx, "opensearch"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("empty cursor = %v, want ErrNoRows", err)
	}
	for range 3 {
		if err := r.InsertAuditEvent(ctx, db.InsertAuditEventParams{
			ActorKind: "service", ActorID: &actor, Action: "metadata.sync", ResourceType: "catalog", Outcome: "allowed",
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC().Add(time.Minute)
	batch, err := r.ListAuditEventsForExport(ctx, db.ListAuditEventsForExportParams{
		AfterAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
		Before:  pgtype.Timestamptz{Time: before, Valid: true}, Lim: 10,
	})
	if err != nil || len(batch) != 3 {
		t.Fatalf("export batch = %d, %v", len(batch), err)
	}
	if batch[0].ID > batch[1].ID || batch[1].ID > batch[2].ID {
		t.Fatalf("export is not oldest first: %+v", batch)
	}
	if batch[0].ActorID != actor || batch[0].Action != "metadata.sync" || batch[0].ActorKind != "service" {
		t.Fatalf("export row = %+v", batch[0])
	}

	pos := batch[1]
	if err := r.SetAuditExportCursor(ctx, db.SetAuditExportCursorParams{
		Sink: "opensearch", OccurredAt: pos.OccurredAt, ID: pos.ID,
	}); err != nil {
		t.Fatalf("SetAuditExportCursor: %v", err)
	}
	got, err := r.GetAuditExportCursor(ctx, "opensearch")
	if err != nil || got.ID != pos.ID || !got.OccurredAt.Time.Equal(pos.OccurredAt.Time) {
		t.Fatalf("cursor = %+v, %v", got, err)
	}
	after, err := r.ListAuditEventsForExport(ctx, db.ListAuditEventsForExportParams{
		AfterAt: got.OccurredAt, AfterID: got.ID, Before: pgtype.Timestamptz{Time: before, Valid: true}, Lim: 10,
	})
	if err != nil || len(after) != 1 || after[0].ID <= pos.ID {
		t.Fatalf("batch after cursor = %+v, %v", after, err)
	}
}
