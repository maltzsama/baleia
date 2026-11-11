package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
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

	stmts, _, err := f.r.OffloadQueryStatements(ctx, []model.UUID{e1.ID}, base.Add(-time.Hour), base.Add(time.Hour), 1000)
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

// TestDeleteQueryHistoryDependents: the period-scoped deletes remove the rows
// that would otherwise block detaching one day's query-history partitions, and
// only that day's: an execution of the next day keeps its references, joins and
// snapshot link.
func TestDeleteQueryHistoryDependents(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	today := partitioning.Daily.Start(time.Now()).Add(10 * time.Hour)
	tomorrow := today.AddDate(0, 0, 1)

	// addDay creates an execution that started at when, with a statement, a
	// reference, a join and a snapshot linked to it.
	addDay := func(when time.Time, trinoID string) model.UUID {
		t.Helper()
		exec := model.QueryExecution{
			ID: model.UUID(NewUUID()), WorkspaceID: f.ws, UserID: f.owner,
			Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: when,
		}
		if err := f.gdb.Create(&exec).Error; err != nil {
			t.Fatalf("create execution: %v", err)
		}
		stmt := model.QueryStatement{
			ID: model.UUID(NewUUID()), ExecutionID: exec.ID, ExecutionStartedAt: when,
			Position: 1, SQL: "select 1", Status: "finished", StartedAt: when, ResultSets: model.JSON("[]"),
		}
		if err := f.gdb.Create(&stmt).Error; err != nil {
			t.Fatalf("create statement: %v", err)
		}
		ref := model.StatementReference{
			StatementID: stmt.ID, ExecutionStartedAt: when, WorkspaceID: f.ws,
			Namespace: "public", TableNameValue: "t", Alias: "t", Kind: "read",
		}
		if err := f.gdb.Create(&ref).Error; err != nil {
			t.Fatalf("create reference: %v", err)
		}
		join := model.StatementJoin{
			StatementID: stmt.ID, ExecutionStartedAt: when, WorkspaceID: f.ws,
			LeftAlias: "a", LeftColumn: "x", RightAlias: "b", RightColumn: "y",
		}
		if err := f.gdb.Create(&join).Error; err != nil {
			t.Fatalf("create join: %v", err)
		}
		snap := model.TrinoQuerySnapshot{
			ID: model.UUID(NewUUID()), TrinoQueryID: model.Text(trinoID), Origin: "baleia", User: "u",
			State: "finished", SQL: "select 1", EngineID: f.engine, ExecutionID: &exec.ID, ExecutionStartedAt: &when,
		}
		if err := f.gdb.Create(&snap).Error; err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
		return exec.ID
	}
	addDay(today, "tq-today")
	addDay(tomorrow, "tq-tomorrow")

	// Clean up today only.
	from := partitioning.Daily.Start(today)
	until := partitioning.Daily.Next(today)
	if err := f.r.DeleteStatementReferencesByPeriod(ctx, from, until); err != nil {
		t.Fatalf("delete references: %v", err)
	}
	if err := f.r.DeleteStatementJoinsByPeriod(ctx, from, until); err != nil {
		t.Fatalf("delete joins: %v", err)
	}
	if err := f.r.UnlinkTrinoQuerySnapshotsByPeriod(ctx, from, until); err != nil {
		t.Fatalf("unlink snapshots: %v", err)
	}

	count := func(m any, lo, hi time.Time) int64 {
		t.Helper()
		var n int64
		if err := f.gdb.Model(m).Where("execution_started_at >= ? AND execution_started_at < ?", lo, hi).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, m := range []any{&model.StatementReference{}, &model.StatementJoin{}} {
		if n := count(m, from, until); n != 0 {
			t.Errorf("%T of the cleaned day left: %d", m, n)
		}
		if n := count(m, until, partitioning.Daily.Next(tomorrow)); n != 1 {
			t.Errorf("%T of the next day = %d, want 1 untouched", m, n)
		}
	}
	// The cleaned day's snapshot survives, unlinked from its execution, so its
	// metrics are not discarded by query-history retention. The next day's stays
	// linked.
	var snaps []model.TrinoQuerySnapshot
	if err := f.gdb.Order("trino_query_id").Find(&snaps).Error; err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots = %d, %v; want both kept", len(snaps), err)
	}
	byTrinoID := map[string]model.TrinoQuerySnapshot{}
	for _, sn := range snaps {
		byTrinoID[string(sn.TrinoQueryID)] = sn
	}
	if byTrinoID["tq-today"].ExecutionID != nil {
		t.Errorf("the cleaned day's snapshot is still linked: %v", byTrinoID["tq-today"].ExecutionID)
	}
	if byTrinoID["tq-tomorrow"].ExecutionID == nil {
		t.Error("the next day's snapshot lost its link")
	}
}
