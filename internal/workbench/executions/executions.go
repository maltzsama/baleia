// Package executions runs SQL on an engine target through the query
// execution workflow, and reads, cancels and lists the runs. The caller
// resolves and authorizes the target and checks access to the workspace.
package executions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

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

// Runs persists executions; *db.Queries implements it.
type Runs interface {
	CreateQueryExecution(ctx context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error)
	UpdateQueryExecution(ctx context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error)
	GetQueryExecutionByIDAndWorkspace(ctx context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error)
	CancelQueryExecution(ctx context.Context, arg db.CancelQueryExecutionParams) (db.QueryExecution, error)
	ListRunningStatementHandles(ctx context.Context, executionID pgtype.UUID) ([]db.ListRunningStatementHandlesRow, error)
}

// History reads past executions and their statements; *db.Queries
// implements it.
type History interface {
	ListExecutionHistory(ctx context.Context, arg db.ListExecutionHistoryParams) ([]db.ListExecutionHistoryRow, error)
	IsWorkspaceOwner(ctx context.Context, arg db.IsWorkspaceOwnerParams) (bool, error)
	ListQueryStatementsByWorkspace(ctx context.Context, arg db.ListQueryStatementsByWorkspaceParams) ([]db.QueryStatement, error)
	GetQueryStatementByPosition(ctx context.Context, arg db.GetQueryStatementByPositionParams) (db.QueryStatement, error)
}

// Store is Runs and History.
type Store interface {
	Runs
	History
}

// Workflows starts and cancels Temporal workflows; client.Client implements it.
type Workflows interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
	CancelWorkflow(ctx context.Context, workflowID, runID string) error
}

// Engines finds the engine an execution ran on; the engine manager
// implements it.
type Engines interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
	DefaultTrino(ctx context.Context) (queryengine.Engine, error)
}

// Dialects says how an engine kind splits SQL; *dialect.Registry implements it.
type Dialects interface {
	ForEngineKind(kind string) (dialect.Dialect, bool)
}

// Config are the execution settings, resolved by the caller.
type Config struct {
	StatementTimeout time.Duration
	ExecutionTimeout time.Duration
}

// ErrNotFound: no execution (or statement) has this id in the workspace.
var ErrNotFound = errors.New("executions: not found")

// TaskQueue is the worker queue the execution workflow runs on.
const TaskQueue = "baleia-task-queue"

// Service runs and reads executions.
type Service struct {
	store     Store
	workflows Workflows
	engines   Engines
	dialects  Dialects
	cfg       Config
}

// New builds the service. engines may be nil, in which case a cancel only
// stops the workflow; dialects may be nil, in which case every script is
// split as Trino.
func New(store Store, workflows Workflows, engines Engines, dialects Dialects, cfg Config) *Service {
	return &Service{store: store, workflows: workflows, engines: engines, dialects: dialects, cfg: cfg}
}

// Run is one execution to start on a resolved target.
type Run struct {
	WorkspaceID  pgtype.UUID
	UserID       pgtype.UUID
	SavedQueryID pgtype.UUID
	SQL          string
	Target       queryengine.Target
	// Identity is who the engine sees: the per-user identity and, for an
	// agent, its id.
	Identity  queryengine.Identity
	AgentID   pgtype.UUID
	RequestID string
	// RerunOf copies the statements of a previous execution from
	// RerunFromPosition instead of re-splitting SQL (#111).
	RerunOf           pgtype.UUID
	RerunFromPosition int32
}

// Start records the execution as running and starts its workflow. When the
// workflow cannot start, the row is failed so it never stays running.
func (s *Service) Start(ctx context.Context, r Run) (db.QueryExecution, error) {
	snapshot, err := r.Target.Snapshot()
	if err != nil {
		return db.QueryExecution{}, err
	}
	catalog := r.Target.EngineCatalog
	var schema *string
	if r.Target.Schema != "" {
		schema = &r.Target.Schema
	}
	var requestID *string
	if r.RequestID != "" {
		requestID = &r.RequestID
	}

	execution, err := s.store.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID:  r.WorkspaceID,
		UserID:       r.UserID,
		SavedQueryID: r.SavedQueryID,
		Status:       "running",
		Sql:          r.SQL,
		TrinoCatalog: &catalog,
		RequestID:    requestID,
		// The resolved target, recorded for history (#13).
		EngineID:        r.Target.EngineID,
		TargetBindingID: r.Target.BindingID,
		TargetCatalogID: r.Target.CatalogID,
		TargetSchema:    schema,
		TargetSnapshot:  snapshot,
		AgentID:         r.AgentID,
	})
	if err != nil {
		return db.QueryExecution{}, err
	}

	executionID := uuid.UUID(execution.ID.Bytes).String()
	// The script is split with the target engine's dialect; Trino when the
	// kind has none.
	dialectName := dialect.Trino
	if s.dialects != nil {
		if d, ok := s.dialects.ForEngineKind(r.Target.EngineKind); ok {
			dialectName = d.Name()
		}
	}
	rerunOf := ""
	if r.RerunOf.Valid {
		rerunOf = uuid.UUID(r.RerunOf.Bytes).String()
	}
	_, err = s.workflows.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: WorkflowID(execution.ID), TaskQueue: TaskQueue},
		workflows.QueryExecutionWorkflowV3, workflows.QueryExecutionInput{
			ExecutionID:       executionID,
			WorkspaceID:       uuid.UUID(r.WorkspaceID.Bytes).String(),
			SQL:               r.SQL,
			Target:            r.Target,
			Identity:          r.Identity,
			StatementTimeout:  s.cfg.StatementTimeout,
			RequestID:         r.RequestID,
			ExecutionTimeout:  s.cfg.ExecutionTimeout,
			RerunExecutionID:  rerunOf,
			RerunFromPosition: r.RerunFromPosition,
			Dialect:           dialectName,
		})
	if err != nil {
		// Detached from the request, which may be the reason it failed.
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, ferr := s.store.UpdateQueryExecution(failCtx, db.UpdateQueryExecutionParams{ID: execution.ID, Status: "failed"}); ferr != nil && !errors.Is(ferr, pgx.ErrNoRows) {
			slog.Error("failed to mark execution failed after workflow start error",
				"execution_id", executionID, "start_err", err, "mark_err", ferr)
		}
		return db.QueryExecution{}, err
	}
	return execution, nil
}

// WorkflowID is the id of an execution's workflow.
func WorkflowID(executionID pgtype.UUID) string {
	return "exec-" + uuid.UUID(executionID.Bytes).String()
}

// Get returns one execution of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.QueryExecution, error) {
	e, err := s.store.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{ID: id, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.QueryExecution{}, ErrNotFound
	}
	return e, err
}

// Cancel stops a running execution: only a running row becomes cancelled, so
// a completion racing this call is never overwritten. It then cancels the
// workflow and, best effort, the statements still running on the engine.
// cancelled is false when the execution had already finished; the execution
// is then returned as it is.
func (s *Service) Cancel(ctx context.Context, ws, id pgtype.UUID) (execution db.QueryExecution, cancelled bool, err error) {
	updated, err := s.store.CancelQueryExecution(ctx, db.CancelQueryExecutionParams{ID: id, WorkspaceID: ws})
	if err == nil {
		// A NotFound means the workflow already ended.
		cancelErr := s.workflows.CancelWorkflow(ctx, WorkflowID(id), "")
		var notFound *serviceerror.NotFound
		if cancelErr != nil && !errors.As(cancelErr, &notFound) {
			slog.Warn("failed to cancel temporal workflow", "execution_id", id.String(), "err", cancelErr)
		}
		// The worker running it may be gone: stop the statements on the
		// engine through their persisted handles too.
		s.cancelOnEngine(ctx, updated)
		return updated, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.QueryExecution{}, false, err
	}
	current, err := s.Get(ctx, ws, id)
	if err != nil {
		return db.QueryExecution{}, false, err
	}
	return current, false, nil
}

// cancelOnEngine cancels the running statements of an execution by their
// persisted handles, best effort and bounded in time.
func (s *Service) cancelOnEngine(ctx context.Context, e db.QueryExecution) {
	if s.engines == nil {
		return
	}
	rows, err := s.store.ListRunningStatementHandles(ctx, e.ID)
	if err != nil || len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var eng queryengine.Engine
	if e.EngineID.Valid {
		eng, err = s.engines.Get(ctx, e.EngineID)
	} else {
		eng, err = s.engines.DefaultTrino(ctx)
	}
	if err != nil {
		slog.Warn("cancel on engine: no engine", "execution_id", e.ID.String(), "err", err)
		return
	}
	for _, r := range rows {
		var handle queryengine.QueryHandle
		if json.Unmarshal(r.QueryHandle, &handle) != nil {
			continue
		}
		if err := eng.Cancel(ctx, handle); err != nil {
			slog.Warn("cancel on engine failed", "execution_id", e.ID.String(), "position", r.Position, "err", err)
		}
	}
}

// Statements returns the statements of an execution of the workspace. A
// foreign or absent execution is ErrNotFound, never an empty list.
func (s *Service) Statements(ctx context.Context, ws, id pgtype.UUID) ([]db.QueryStatement, error) {
	if _, err := s.Get(ctx, ws, id); err != nil {
		return nil, err
	}
	return s.store.ListQueryStatementsByWorkspace(ctx, db.ListQueryStatementsByWorkspaceParams{ExecutionID: id, WorkspaceID: ws})
}

// Statement returns one statement of an execution of the workspace.
func (s *Service) Statement(ctx context.Context, ws, executionID pgtype.UUID, position int32) (db.QueryStatement, error) {
	st, err := s.store.GetQueryStatementByPosition(ctx, db.GetQueryStatementByPositionParams{ExecutionID: executionID, WorkspaceID: ws, Position: position})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.QueryStatement{}, ErrNotFound
	}
	return st, err
}

// Viewer is who lists the history.
type Viewer struct {
	UserID pgtype.UUID
	// Admin is a platform administrator.
	Admin bool
}

// Page is one page of history: Rows, and More when another page follows.
type Page struct {
	Rows []db.ListExecutionHistoryRow
	More bool
}

// History lists a page of the workspace's executions, newest first. The
// workspace owner and administrators see every execution and may filter by
// user (q.UserID); everyone else sees only their own (#36).
func (s *Service) History(ctx context.Context, v Viewer, q db.ListExecutionHistoryParams, limit int) (Page, error) {
	seeAll := v.Admin
	if !seeAll {
		owner, err := s.store.IsWorkspaceOwner(ctx, db.IsWorkspaceOwnerParams{ID: q.WorkspaceID, UserID: v.UserID})
		if err != nil {
			return Page{}, err
		}
		seeAll = owner
	}
	if !seeAll {
		q.UserID = v.UserID
	}
	if limit < 1 || limit >= math.MaxInt32 {
		return Page{}, fmt.Errorf("executions: page size %d out of range", limit)
	}
	// One extra row says whether another page follows.
	q.Lim = int32(limit + 1)
	rows, err := s.store.ListExecutionHistory(ctx, q)
	if err != nil {
		return Page{}, err
	}
	if len(rows) > limit {
		return Page{Rows: rows[:limit], More: true}, nil
	}
	return Page{Rows: rows}, nil
}
