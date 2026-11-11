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

	stmts, err := f.r.OffloadQueryStatements(ctx, []model.UUID{e1.ID}, base.Add(-time.Hour), base.Add(time.Hour))
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

// TestDeleteQueryHistoryDependents: the month-scoped deletes remove the rows
// that would otherwise block detaching a month's query-history partitions.
func TestDeleteQueryHistoryDependents(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	exec := model.QueryExecution{
		ID: model.UUID(NewUUID()), WorkspaceID: f.ws, UserID: f.owner,
		Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: base,
	}
	if err := f.gdb.Create(&exec).Error; err != nil {
		t.Fatalf("create execution: %v", err)
	}
	stmt := model.QueryStatement{
		ID: model.UUID(NewUUID()), ExecutionID: exec.ID, ExecutionStartedAt: base,
		Position: 1, SQL: "select 1", Status: "finished", StartedAt: base, ResultSets: model.JSON("[]"),
	}
	if err := f.gdb.Create(&stmt).Error; err != nil {
		t.Fatalf("create statement: %v", err)
	}
	ref := model.StatementReference{
		StatementID: stmt.ID, ExecutionStartedAt: base, WorkspaceID: f.ws,
		Namespace: "public", TableNameValue: "t", Alias: "t", Kind: "read",
	}
	if err := f.gdb.Create(&ref).Error; err != nil {
		t.Fatalf("create reference: %v", err)
	}
	join := model.StatementJoin{
		StatementID: stmt.ID, ExecutionStartedAt: base, WorkspaceID: f.ws,
		LeftAlias: "a", LeftColumn: "x", RightAlias: "b", RightColumn: "y",
	}
	if err := f.gdb.Create(&join).Error; err != nil {
		t.Fatalf("create join: %v", err)
	}
	snap := model.TrinoQuerySnapshot{
		ID: model.UUID(NewUUID()), TrinoQueryID: "tq1", Origin: "baleia", User: "u",
		State: "finished", SQL: "select 1", EngineID: f.engine, ExecutionID: &exec.ID, ExecutionStartedAt: &base,
	}
	if err := f.gdb.Create(&snap).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	from := time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC)
	until := from.AddDate(0, 1, 0)
	if err := f.r.DeleteStatementReferencesByMonth(ctx, from, until); err != nil {
		t.Fatalf("delete references: %v", err)
	}
	if err := f.r.DeleteStatementJoinsByMonth(ctx, from, until); err != nil {
		t.Fatalf("delete joins: %v", err)
	}
	if err := f.r.DeleteTrinoQuerySnapshotsByMonth(ctx, from, until); err != nil {
		t.Fatalf("delete snapshots: %v", err)
	}

	var refs, joins, snaps int64
	f.gdb.Model(&model.StatementReference{}).Count(&refs)
	f.gdb.Model(&model.StatementJoin{}).Count(&joins)
	f.gdb.Model(&model.TrinoQuerySnapshot{}).Count(&snaps)
	if refs != 0 || joins != 0 || snaps != 0 {
		t.Fatalf("dependents left: references=%d joins=%d snapshots=%d", refs, joins, snaps)
	}
}
