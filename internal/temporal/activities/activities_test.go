package activities

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
)

// fakeQuerier embeds db.Querier so only the exercised method is implemented.
type fakeQuerier struct {
	db.Querier
	updateErr error
}

func (f *fakeQuerier) UpdateQueryExecution(context.Context, db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	return db.QueryExecution{}, f.updateErr
}

const testExecID = "00000000-0000-0000-0000-000000000001"

// TestCompleteExecutionDoesNotOverwriteTerminal pins the race fix: when the
// conditional update matches nothing (the execution was cancelled), the activity
// must succeed without overwriting the terminal status.
func TestCompleteExecutionDoesNotOverwriteTerminal(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: pgx.ErrNoRows}}

	err := a.completeExecution(context.Background(), CompleteExecutionParams{
		ExecutionID: testExecID,
		Status:      "finished",
	})
	if err != nil {
		t.Fatalf("completeExecution must not fail when the execution is already terminal: %v", err)
	}
}

func TestFailExecutionDoesNotOverwriteTerminal(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: pgx.ErrNoRows}}

	err := a.failExecution(context.Background(), FailExecutionParams{
		ExecutionID: testExecID,
	})
	if err != nil {
		t.Fatalf("failExecution must not fail when the execution is already terminal: %v", err)
	}
}

func TestCompleteExecutionPropagatesRealErrors(t *testing.T) {
	a := &Activities{queries: &fakeQuerier{updateErr: context.DeadlineExceeded}}

	err := a.completeExecution(context.Background(), CompleteExecutionParams{
		ExecutionID: testExecID,
		Status:      "finished",
	})
	if err == nil {
		t.Fatal("completeExecution must propagate non-ErrNoRows errors")
	}
}
