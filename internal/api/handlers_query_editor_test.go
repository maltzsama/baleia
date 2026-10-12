package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5"
)

// fakeQuerier embeds db.Querier so only the methods a test exercises need an
// implementation; any other call panics on the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	hasWorkspaceAccess bool
	execErr            error
	exec               db.QueryExecution
	gotExecParams      *db.GetQueryExecutionByIDAndWorkspaceParams
	cancelErr          error
	cancelResult       db.QueryExecution
	statements         []db.QueryStatement
}

func (f *fakeQuerier) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return f.hasWorkspaceAccess, nil
}

func (f *fakeQuerier) GetQueryExecutionByIDAndWorkspace(_ context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error) {
	f.gotExecParams = &arg
	if f.execErr != nil {
		return db.QueryExecution{}, f.execErr
	}
	return f.exec, nil
}

func (f *fakeQuerier) CancelQueryExecution(context.Context, db.CancelQueryExecutionParams) (db.QueryExecution, error) {
	return f.cancelResult, f.cancelErr
}

func (f *fakeQuerier) ListQueryStatementsByWorkspace(context.Context, db.ListQueryStatementsByWorkspaceParams) ([]db.QueryStatement, error) {
	return f.statements, nil
}

const testUserID = "11111111-1111-1111-1111-111111111111"

func testContext() context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyUserID, testUserID)
}

func execRequest() (PathId, openapi_types.UUID) {
	return PathId{0xAA}, openapi_types.UUID{0xBB}
}

// TestGetExecutionRejectsForeignWorkspace pins the leak fix: an execution that
// belongs to another workspace yields pgx.ErrNoRows from the scoped query, so
// the handler answers 404 instead of returning the foreign execution.
func TestGetExecutionRejectsForeignWorkspace(t *testing.T) {
	fake := &fakeQuerier{hasWorkspaceAccess: true, execErr: pgx.ErrNoRows}
	h := &Handlers{queries: fake}
	wsID, execID := execRequest()

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution404JSONResponse); !ok {
		t.Fatalf("want 404 for a foreign execution, got %T", resp)
	}
	if fake.gotExecParams == nil {
		t.Fatal("scoped lookup was not called")
	}
	if got := fake.gotExecParams.WorkspaceID.Bytes[0]; got != byte(0xAA) {
		t.Fatalf("query workspace = %#x, want 0xaa (the request workspace must scope the lookup)", got)
	}
}

func TestGetExecutionRequiresWorkspaceAccess(t *testing.T) {
	h := &Handlers{queries: &fakeQuerier{hasWorkspaceAccess: false}}
	wsID, execID := execRequest()

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution401JSONResponse); !ok {
		t.Fatalf("want 401 without workspace access, got %T", resp)
	}
}

func TestGetExecutionReturnsOwnExecution(t *testing.T) {
	h := &Handlers{queries: &fakeQuerier{hasWorkspaceAccess: true}}
	wsID, execID := execRequest()

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution200JSONResponse); !ok {
		t.Fatalf("want 200 for own execution, got %T", resp)
	}
}

func TestCancelExecutionCancelsRunning(t *testing.T) {
	fake := &fakeQuerier{hasWorkspaceAccess: true, cancelResult: db.QueryExecution{Status: "cancelled"}}
	h := &Handlers{queries: fake}
	wsID, execID := execRequest()

	resp, err := h.CancelExecution(testContext(), CancelExecutionRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(CancelExecution200JSONResponse)
	if !ok {
		t.Fatalf("want 200, got %T", resp)
	}
	if got.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
}

// TestCancelExecutionDoesNotOverrideCompletion pins the race fix: when the
// conditional cancel matches nothing (the query already finished), the handler
// returns the real terminal status instead of reporting cancelled.
func TestCancelExecutionDoesNotOverrideCompletion(t *testing.T) {
	fake := &fakeQuerier{
		hasWorkspaceAccess: true,
		cancelErr:          pgx.ErrNoRows,
		exec:               db.QueryExecution{Status: "finished"},
	}
	h := &Handlers{queries: fake}
	wsID, execID := execRequest()

	resp, err := h.CancelExecution(testContext(), CancelExecutionRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(CancelExecution200JSONResponse)
	if !ok {
		t.Fatalf("want 200, got %T", resp)
	}
	if got.Status != "finished" {
		t.Fatalf("status = %q, want finished (the completion must win the race)", got.Status)
	}
}

func TestListStatementsReturns404ForForeignExecution(t *testing.T) {
	h := &Handlers{queries: &fakeQuerier{hasWorkspaceAccess: true, execErr: pgx.ErrNoRows}}
	wsID, execID := execRequest()

	resp, err := h.ListStatements(testContext(), ListStatementsRequestObject{Id: wsID, ExecutionId: execID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(ListStatements404JSONResponse); !ok {
		t.Fatalf("want 404 for a foreign execution, got %T", resp)
	}
}
