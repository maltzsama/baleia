package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
)

// TestOffloadQueryHistory covers the offload read of query_executions (ordered
// by started_at, id) and of one execution's statements, plus the cursor's
// string tiebreaker for the UUID-keyed query_history stream (ADR 0024).
func TestOffloadQueryHistory(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second) // the current month has a partition

	mkExec := func(id string, at time.Time) model.QueryExecution {
		return model.QueryExecution{
			ID: model.UUID(id), WorkspaceID: f.ws, UserID: f.owner,
			Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: at,
		}
	}
	e1 := mkExec(NewUUID(), base)
	e2 := mkExec(NewUUID(), base.Add(time.Minute))
	e3 := mkExec(NewUUID(), base.Add(2*time.Minute))
	for _, e := range []model.QueryExecution{e1, e2, e3} {
		if err := f.gdb.Create(&e).Error; err != nil {
			t.Fatalf("create execution: %v", err)
		}
	}
	stmt := model.QueryStatement{
		ID: model.UUID(NewUUID()), ExecutionID: e1.ID, ExecutionStartedAt: e1.StartedAt,
		Position: 1, SQL: "select 1", Status: "finished", StartedAt: e1.StartedAt,
		ResultSets: model.JSON("[]"),
	}
	if err := f.gdb.Create(&stmt).Error; err != nil {
		t.Fatalf("create statement: %v", err)
	}

	before := base.Add(time.Hour)
	first, err := f.r.OffloadQueryExecutions(ctx, time.Time{}, "", before, 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first page = %d, want 2", len(first))
	}
	if first[0].StartedAt.After(first[1].StartedAt) {
		t.Fatalf("not ordered by started_at")
	}

	rest, err := f.r.OffloadQueryExecutions(ctx, first[1].StartedAt, string(first[1].ID), before, 10)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(rest) != 1 || string(rest[0].ID) != string(e3.ID) {
		t.Fatalf("resumed page = %+v, want only the third execution", rest)
	}

	stmts, err := f.r.OffloadQueryStatements(ctx, e1.ID, e1.StartedAt)
	if err != nil {
		t.Fatalf("statements: %v", err)
	}
	if len(stmts) != 1 || string(stmts[0].ID) != string(stmt.ID) {
		t.Fatalf("statements = %+v", stmts)
	}

	// The query_history cursor carries a UUID tiebreaker in cursor_key.
	key := NewUUID()
	if err := f.r.SetAuditExportCursor(ctx, db.SetAuditExportCursorParams{
		Stream: "query_history", Sink: "s3",
		OccurredAt: db.Timestamptz{Time: base, Valid: true}, CursorKey: key,
	}); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	got, err := f.r.GetAuditExportCursor(ctx, "query_history", "s3")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if got.CursorKey != key {
		t.Fatalf("cursor key = %q, want %q", got.CursorKey, key)
	}
}
