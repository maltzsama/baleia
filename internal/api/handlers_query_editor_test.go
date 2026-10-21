package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
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
	h := &Handlers{queries: fake, temporalClient: &fakeTemporalClient{}}
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

// fakeRunQueryQuerier supports the RunQuery start-failure path: it answers the
// workspace and creates the running execution.
type fakeRunQueryQuerier struct {
	db.Querier
	exec         db.QueryExecution
	markStatuses []string
	workspace    db.Workspace
	targets      []db.ListWorkspaceTargetsRow
	created      []db.CreateQueryExecutionParams
	agentBound   bool
}

func (f *fakeRunQueryQuerier) ListWorkspaceTargets(context.Context, pgtype.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	return f.targets, nil
}

func (f *fakeRunQueryQuerier) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return true, nil
}

func (f *fakeRunQueryQuerier) GetWorkspaceByID(context.Context, pgtype.UUID) (db.Workspace, error) {
	return f.workspace, nil
}

func (f *fakeRunQueryQuerier) AgentWorkspaceBound(context.Context, db.AgentWorkspaceBoundParams) (bool, error) {
	return f.agentBound, nil
}

func (f *fakeRunQueryQuerier) CreateQueryExecution(_ context.Context, p db.CreateQueryExecutionParams) (db.QueryExecution, error) {
	f.created = append(f.created, p)
	return f.exec, nil
}

func (f *fakeRunQueryQuerier) UpdateQueryExecution(_ context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	f.markStatuses = append(f.markStatuses, arg.Status)
	return f.exec, nil
}

// fakeTemporalClient lets a test make ExecuteWorkflow fail at start or accept
// CancelWorkflow without a real Temporal server.
type fakeTemporalClient struct {
	client.Client
	startErr error
	started  []any // the args of the last ExecuteWorkflow
}

func (f *fakeTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, wf any, args ...any) (client.WorkflowRun, error) {
	f.started = args
	return nil, f.startErr
}

func (f *fakeTemporalClient) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
	return nil
}

// TestRunQuery_WorkflowStartFailureFailsRow pins the orphan-row fix: when the
// workflow cannot start, the running execution row is failed instead of stuck.
func TestRunQuery_WorkflowStartFailureFailsRow(t *testing.T) {
	fake := &fakeRunQueryQuerier{
		exec:    db.QueryExecution{ID: pgtype.UUID{Bytes: [16]byte{0xBB}, Valid: true}, Status: "running"},
		targets: []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true)},
	}
	h := &Handlers{
		queries:        fake,
		cfg:            &config.Config{StatementTimeout: 30 * time.Minute},
		temporalClient: &fakeTemporalClient{startErr: errors.New("temporal down")},
	}

	_, err := h.RunQuery(testContext(), RunQueryRequestObject{
		Id:   PathId{0xAA},
		Body: &RunQueryJSONRequestBody{Sql: "SELECT 1"},
	})
	if err == nil {
		t.Fatal("expected an error when the workflow fails to start")
	}

	if len(fake.markStatuses) != 1 || fake.markStatuses[0] != "failed" {
		t.Fatalf("execution row mark statuses = %v, want [failed]", fake.markStatuses)
	}
}

type handlesQuerier struct {
	*fakeQuerier
	rows []db.ListRunningStatementHandlesRow
}

func (q *handlesQuerier) ListRunningStatementHandles(context.Context, pgtype.UUID) ([]db.ListRunningStatementHandlesRow, error) {
	return q.rows, nil
}

// cancelRecorder is an engine that records Cancel calls.
type cancelRecorder struct {
	queryengine.Engine
	handles []queryengine.QueryHandle
}

func (c *cancelRecorder) Cancel(_ context.Context, h queryengine.QueryHandle) error {
	c.handles = append(c.handles, h)
	return nil
}

// TestCancelExecutionCancelsOnTheEngine: the running statement is also
// cancelled through its persisted handle, so a dead worker cannot leave it
// running.
func TestCancelExecutionCancelsOnTheEngine(t *testing.T) {
	q := &handlesQuerier{
		fakeQuerier: &fakeQuerier{hasWorkspaceAccess: true, cancelResult: db.QueryExecution{ID: pgtype.UUID{Bytes: [16]byte{0xBB}, Valid: true}, Status: "cancelled"}},
		rows:        []db.ListRunningStatementHandlesRow{{Position: 1, QueryHandle: []byte(`{"engine_id":"e","query_id":"20260101_000000_00001_abcde"}`)}},
	}
	rec := &cancelRecorder{}
	h := &Handlers{queries: q, temporalClient: &fakeTemporalClient{}, engines: fixedEngines{rec}}
	wsID, execID := execRequest()
	if _, err := h.CancelExecution(testContext(), CancelExecutionRequestObject{Id: wsID, ExecutionId: execID}); err != nil {
		t.Fatal(err)
	}
	if len(rec.handles) != 1 || rec.handles[0].QueryID != "20260101_000000_00001_abcde" {
		t.Fatalf("engine cancels = %+v", rec.handles)
	}
}
