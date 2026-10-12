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
	gotExecParams      *db.GetQueryExecutionByIDAndWorkspaceParams
}

func (f *fakeQuerier) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return f.hasWorkspaceAccess, nil
}

func (f *fakeQuerier) GetQueryExecutionByIDAndWorkspace(_ context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error) {
	f.gotExecParams = &arg
	if f.execErr != nil {
		return db.QueryExecution{}, f.execErr
	}
	return db.QueryExecution{}, nil
}

const testUserID = "11111111-1111-1111-1111-111111111111"

func testContext() context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyUserID, testUserID)
}

// TestGetExecutionRejectsForeignWorkspace pins the leak fix: an execution that
// belongs to another workspace yields pgx.ErrNoRows from the scoped query, so
// the handler answers 404 instead of returning the foreign execution.
func TestGetExecutionRejectsForeignWorkspace(t *testing.T) {
	fake := &fakeQuerier{hasWorkspaceAccess: true, execErr: pgx.ErrNoRows}
	h := &Handlers{queries: fake}

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{
		Id:          PathId{0xAA},
		ExecutionId: openapi_types.UUID{0xBB},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution404JSONResponse); !ok {
		t.Fatalf("want 404 for a foreign execution, got %T", resp)
	}
	if fake.gotExecParams == nil {
		t.Fatal("scoped lookup was not called")
	}
	if got, want := fake.gotExecParams.WorkspaceID.Bytes[0], byte(0xAA); got != want {
		t.Fatalf("query workspace = %#x, want %#x (request workspace must scope the lookup)", got, want)
	}
}

func TestGetExecutionRequiresWorkspaceAccess(t *testing.T) {
	h := &Handlers{queries: &fakeQuerier{hasWorkspaceAccess: false}}

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{
		Id:          PathId{0xAA},
		ExecutionId: openapi_types.UUID{0xBB},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution401JSONResponse); !ok {
		t.Fatalf("want 401 without workspace access, got %T", resp)
	}
}

func TestGetExecutionReturnsOwnExecution(t *testing.T) {
	h := &Handlers{queries: &fakeQuerier{hasWorkspaceAccess: true}}

	resp, err := h.GetExecution(testContext(), GetExecutionRequestObject{
		Id:          PathId{0xAA},
		ExecutionId: openapi_types.UUID{0xBB},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetExecution200JSONResponse); !ok {
		t.Fatalf("want 200 for own execution, got %T", resp)
	}
}
