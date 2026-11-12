package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
)

// TestExecutionHistorySearchesTheSQL: the history filters on the text of the
// query, ignoring case, on every backend. `sql` is a reserved word in MySQL, so
// the column has to reach the database as a quoted identifier.
func TestExecutionHistorySearchesTheSQL(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	today := partitioning.Daily.Start(time.Now()).Add(10 * time.Hour)

	for i, sql := range []string{"SELECT a FROM Orders", "select b from customers"} {
		exec := model.QueryExecution{
			ID: model.UUID(NewUUID()), WorkspaceID: f.ws, UserID: f.owner,
			Status: "finished", Origin: "editor", SQL: sql, StartedAt: today.Add(time.Duration(i) * time.Minute),
		}
		if err := f.gdb.Create(&exec).Error; err != nil {
			t.Fatalf("create execution: %v", err)
		}
	}

	search := func(q string) []string {
		t.Helper()
		page, err := f.r.ListExecutionHistory(ctx, db.ListExecutionHistoryParams{WorkspaceID: f.wsID(), Q: &q, Lim: 10})
		if err != nil {
			t.Fatalf("ListExecutionHistory(%q): %v", q, err)
		}
		out := make([]string, len(page))
		for i, row := range page {
			out[i] = row.Sql
		}
		return out
	}
	if got := search("orders"); len(got) != 1 || got[0] != "SELECT a FROM Orders" {
		t.Errorf(`search "orders" = %v, want the Orders query (case-insensitive)`, got)
	}
	if got := search("CUSTOM"); len(got) != 1 || got[0] != "select b from customers" {
		t.Errorf(`search "CUSTOM" = %v, want the customers query (case-insensitive)`, got)
	}
	if got := search("select"); len(got) != 2 {
		t.Errorf(`search "select" = %v, want both`, got)
	}
	if got := search("nothing like this"); len(got) != 0 {
		t.Errorf("search for text no query has = %v, want none", got)
	}
}
