package executions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	execs   map[[16]byte]db.QueryExecution
	handles []db.ListRunningStatementHandlesRow
	owner   bool
	// lastHistory is the query History sent.
	lastHistory db.ListExecutionHistoryParams
	history     []db.ListExecutionHistoryRow
}

func newStore() *fakeStore { return &fakeStore{execs: map[[16]byte]db.QueryExecution{}} }

func (f *fakeStore) CreateQueryExecution(_ context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error) {
	e := db.QueryExecution{ID: newID(), WorkspaceID: arg.WorkspaceID, UserID: arg.UserID, Status: arg.Status, Sql: arg.Sql, EngineID: arg.EngineID, TargetSchema: arg.TargetSchema, AgentID: arg.AgentID}
	f.execs[e.ID.Bytes] = e
	return e, nil
}

func (f *fakeStore) UpdateQueryExecution(_ context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	e := f.execs[arg.ID.Bytes]
	e.Status = arg.Status
	f.execs[arg.ID.Bytes] = e
	return e, nil
}

func (f *fakeStore) GetQueryExecutionByIDAndWorkspace(_ context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error) {
	if e, ok := f.execs[arg.ID.Bytes]; ok && e.WorkspaceID == arg.WorkspaceID {
		return e, nil
	}
	return db.QueryExecution{}, pgx.ErrNoRows
}

func (f *fakeStore) CancelQueryExecution(_ context.Context, arg db.CancelQueryExecutionParams) (db.QueryExecution, error) {
	e, ok := f.execs[arg.ID.Bytes]
	if !ok || e.WorkspaceID != arg.WorkspaceID || e.Status != "running" {
		return db.QueryExecution{}, pgx.ErrNoRows
	}
	e.Status = "cancelled"
	f.execs[arg.ID.Bytes] = e
	return e, nil
}

func (f *fakeStore) ListRunningStatementHandles(context.Context, pgtype.UUID) ([]db.ListRunningStatementHandlesRow, error) {
	return f.handles, nil
}

func (f *fakeStore) ListExecutionHistory(_ context.Context, arg db.ListExecutionHistoryParams) ([]db.ListExecutionHistoryRow, error) {
	f.lastHistory = arg
	if int(arg.Lim) < len(f.history) {
		return f.history[:arg.Lim], nil
	}
	return f.history, nil
}

func (f *fakeStore) IsWorkspaceOwner(context.Context, db.IsWorkspaceOwnerParams) (bool, error) {
	return f.owner, nil
}

func (f *fakeStore) ListQueryStatementsByWorkspace(context.Context, db.ListQueryStatementsByWorkspaceParams) ([]db.QueryStatement, error) {
	return []db.QueryStatement{{Position: 0}}, nil
}

func (f *fakeStore) GetQueryStatementByPosition(context.Context, db.GetQueryStatementByPositionParams) (db.QueryStatement, error) {
	return db.QueryStatement{}, pgx.ErrNoRows
}

type fakeWorkflows struct {
	started   []workflows.QueryExecutionInput
	ids       []string
	startErr  error
	cancelled []string
	cancelErr error
}

func (w *fakeWorkflows) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
	if w.startErr != nil {
		return nil, w.startErr
	}
	w.ids = append(w.ids, o.ID)
	w.started = append(w.started, args[0].(workflows.QueryExecutionInput))
	return nil, nil
}

func (w *fakeWorkflows) CancelWorkflow(_ context.Context, id, _ string) error {
	w.cancelled = append(w.cancelled, id)
	return w.cancelErr
}

// fakeEngine records the handles it was asked to cancel.
type fakeEngine struct {
	queryengine.Engine
	cancelled []string
}

func (e *fakeEngine) Cancel(_ context.Context, h queryengine.QueryHandle) error {
	e.cancelled = append(e.cancelled, h.QueryID)
	return nil
}

type fakeEngines struct{ e *fakeEngine }

func (f fakeEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) { return f.e, nil }
func (f fakeEngines) DefaultTrino(context.Context) (queryengine.Engine, error)     { return f.e, nil }

func setup() (*Service, *fakeStore, *fakeWorkflows, *fakeEngine) {
	st, wf, eng := newStore(), &fakeWorkflows{}, &fakeEngine{}
	return New(st, wf, fakeEngines{eng}, dialect.NewRegistry(), Config{}), st, wf, eng
}

func TestStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, st, wf, _ := setup()
	ws := newID()
	e, err := s.Start(ctx, Run{
		WorkspaceID: ws, UserID: newID(), SQL: "select 1",
		Target:    queryengine.Target{EngineKind: "postgresql", EngineID: newID(), Schema: "public"},
		RequestID: "req-1",
	})
	if err != nil || e.Status != "running" {
		t.Fatalf("Start = %+v, %v", e, err)
	}
	if len(wf.started) != 1 || wf.ids[0] != WorkflowID(e.ID) {
		t.Fatalf("workflows = %v", wf.ids)
	}
	in := wf.started[0]
	if in.Dialect != dialect.PostgreSQL || in.SQL != "select 1" || in.RequestID != "req-1" || in.ExecutionID != uuid.UUID(e.ID.Bytes).String() {
		t.Errorf("input = %+v", in)
	}
	if st.execs[e.ID.Bytes].TargetSchema == nil || *st.execs[e.ID.Bytes].TargetSchema != "public" {
		t.Error("the resolved target is recorded with the execution")
	}

	// A kind without a dialect splits as Trino.
	if _, err := s.Start(ctx, Run{WorkspaceID: ws, Target: queryengine.Target{EngineKind: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if wf.started[1].Dialect != dialect.Trino {
		t.Errorf("dialect = %q, want trino by default", wf.started[1].Dialect)
	}
}

func TestStart_WorkflowFailureFailsTheRow(t *testing.T) {
	t.Parallel()
	s, st, wf, _ := setup()
	wf.startErr = errors.New("temporal down")
	if _, err := s.Start(context.Background(), Run{WorkspaceID: newID()}); !errors.Is(err, wf.startErr) {
		t.Fatalf("Start = %v, want the start error", err)
	}
	for _, e := range st.execs {
		if e.Status != "failed" {
			t.Errorf("status = %q, want failed so it never stays running", e.Status)
		}
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, st, wf, eng := setup()
	ws := newID()
	e, _ := s.Start(ctx, Run{WorkspaceID: ws, Target: queryengine.Target{EngineID: newID()}})
	handle, _ := json.Marshal(queryengine.QueryHandle{QueryID: "q-7"})
	st.handles = []db.ListRunningStatementHandlesRow{{QueryHandle: handle}}
	wf.cancelErr = serviceerror.NewNotFound("workflow already ended")

	got, cancelled, err := s.Cancel(ctx, ws, e.ID)
	if err != nil || !cancelled || got.Status != "cancelled" {
		t.Fatalf("Cancel = %q, %v, %v", got.Status, cancelled, err)
	}
	if len(wf.cancelled) != 1 || wf.cancelled[0] != WorkflowID(e.ID) {
		t.Errorf("workflow cancels = %v", wf.cancelled)
	}
	if len(eng.cancelled) != 1 || eng.cancelled[0] != "q-7" {
		t.Errorf("engine cancels = %v, want the persisted handle", eng.cancelled)
	}

	// Already terminal: nothing is cancelled, the current state comes back.
	got, cancelled, err = s.Cancel(ctx, ws, e.ID)
	if err != nil || cancelled || got.Status != "cancelled" {
		t.Errorf("second Cancel = %q, %v, %v", got.Status, cancelled, err)
	}
	if _, _, err := s.Cancel(ctx, newID(), e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign workspace = %v, want ErrNotFound", err)
	}
}

func TestStatements_ForeignExecutionIsNotFound(t *testing.T) {
	t.Parallel()
	s, _, _, _ := setup()
	if _, err := s.Statements(context.Background(), newID(), newID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Statements = %v, want ErrNotFound", err)
	}
	if _, err := s.Statement(context.Background(), newID(), newID(), 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Statement = %v, want ErrNotFound", err)
	}
}

func TestHistory_Visibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, st, _, _ := setup()
	me, other := newID(), newID()
	st.history = make([]db.ListExecutionHistoryRow, 3)

	// A member sees only their own, whatever user they ask for.
	page, err := s.History(ctx, Viewer{UserID: me}, db.ListExecutionHistoryParams{UserID: other}, 2)
	if err != nil || st.lastHistory.UserID != me {
		t.Fatalf("member filter = %v, %v; want forced to self", st.lastHistory.UserID, err)
	}
	if len(page.Rows) != 2 || !page.More || st.lastHistory.Lim != 3 {
		t.Errorf("page = %d rows, more %v, asked %d; want limit+1 to detect more", len(page.Rows), page.More, st.lastHistory.Lim)
	}

	// The owner sees everyone and may filter.
	st.owner = true
	if _, _ = s.History(ctx, Viewer{UserID: me}, db.ListExecutionHistoryParams{UserID: other}, 5); st.lastHistory.UserID != other {
		t.Error("the owner may filter by another user")
	}
	st.owner = false
	if _, _ = s.History(ctx, Viewer{UserID: me, Admin: true}, db.ListExecutionHistoryParams{}, 5); st.lastHistory.UserID.Valid {
		t.Error("an admin sees every execution")
	}
}

// TestStart_WithoutDialects: with no registry every script splits as Trino.
func TestStart_WithoutDialects(t *testing.T) {
	t.Parallel()
	st, wf := newStore(), &fakeWorkflows{}
	s := New(st, wf, nil, nil, Config{})
	if _, err := s.Start(context.Background(), Run{WorkspaceID: newID(), Target: queryengine.Target{EngineKind: "postgresql"}}); err != nil {
		t.Fatal(err)
	}
	if wf.started[0].Dialect != dialect.Trino {
		t.Errorf("dialect = %q, want trino", wf.started[0].Dialect)
	}
}
