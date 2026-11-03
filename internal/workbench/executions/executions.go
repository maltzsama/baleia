// Package executions runs SQL on an engine target through the query
// execution workflow, and reads, cancels and lists the runs. The caller
// resolves and authorizes the target and checks access to the workspace.
package executions

import (
	"cmp"
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
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Runs persists executions; *db.Queries implements it.
type Runs interface {
	CreateQueryExecution(ctx context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error)
	UpdateQueryExecution(ctx context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error)
	GetQueryExecutionByIDAndWorkspace(ctx context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error)
	CancelQueryExecution(ctx context.Context, arg db.CancelQueryExecutionParams) (db.QueryExecution, error)
	FailQueryExecution(ctx context.Context, arg db.FailQueryExecutionParams) (db.QueryExecution, error)
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

// Store is Runs, History and the workload queues.
type Store interface {
	Runs
	History
	Queues
}

// Queues reads the per-engine workload capacities and the workload rules
// (#399); *db.Queries implements it.
type Queues interface {
	ListEngineQueueCapacities(ctx context.Context, engineID pgtype.UUID) ([]db.EngineQueueCapacity, error)
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	ListWorkloadRules(ctx context.Context) ([]db.WorkloadRule, error)
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
	// WorkloadEnabled records the workload class on new runs (#399). Off by
	// default; the admission workflow that reads it ships later.
	WorkloadEnabled bool
	// Logger receives the logs; nil discards them (#379).
	Logger *slog.Logger
}

// ErrNotFound: no execution (or statement) has this id in the workspace.
var ErrNotFound = errors.New("executions: not found")

// TaskQueue is the worker queue the execution workflow runs on.
const TaskQueue = "baleia-task-queue"

// Service runs and reads executions.
type Service struct {
	logger    *slog.Logger
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
	return &Service{store: store, workflows: workflows, engines: engines, dialects: dialects, cfg: cfg, logger: cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler))}
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
	// Origin is where the run came from; classification uses it (#399).
	Origin workload.Origin
	// Role is the caller's global role, matched by admin rules (#546).
	Role string
	// TeamID is the workspace owner team, matched by admin rules (#546).
	TeamID pgtype.UUID
	// AgentMaxConcurrent is the agent policy's per-agent concurrency limit,
	// enforced by admission instead of refusing the run (#589).
	AgentMaxConcurrent int
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
	// A-1 tags every run interactive (#527); with workload management on, the
	// classification decides the class and records why (#536). Admin rules are
	// evaluated in order and may set the class, a max class and a weight
	// (#546).
	class := queryengine.ClassInteractive
	weight := 1
	var queueClass, queueReason *string
	if s.cfg.WorkloadEnabled {
		statements := s.splitStatements(r)
		in := r.ruleInput(len(statements))
		// Any DDL in the script keeps it out of background (#580).
		in.DDL = workload.AnyDDL(statements)
		rules := s.workloadRules(ctx)
		var decision workload.Decision
		if matched, ok := workload.MatchRule(in, rules); ok {
			// A rule already decided the class; do not pay for an estimate
			// that cannot change it (#591).
			decision = matched
		} else {
			in.CostHeavy = s.costHeavy(ctx, r, statements)
			decision = workload.Decide(r.Origin, in, nil)
		}
		class = decision.Class
		weight = decision.Weight
		c, reason := string(decision.Class), decision.Reason
		queueClass, queueReason = &c, &reason
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
		QueueClass:      queueClass,
		QueueReason:     queueReason,
	})
	if err != nil {
		return db.QueryExecution{}, err
	}

	executionID := uuid.UUID(execution.ID.Bytes).String()
	// Workload management starts the engine's admission workflow once, before
	// the execution workflow can signal it (#399), and resolves the class's
	// runtime bound for this run (#578).
	var runtimeTimeout time.Duration
	trinoResourceGroups := false
	if s.cfg.WorkloadEnabled && r.Target.EngineID.Valid {
		resolved := s.ensureAdmissionWorkflow(ctx, r.Target)
		runtimeTimeout = resolved[class].RuntimeTimeout
		eng, err := s.store.GetEngineByID(ctx, r.Target.EngineID)
		if err != nil {
			// Without the engine row the admission authority is unknowable, so
			// the run could wait in both queues. Fail it instead of guessing,
			// detached from the request (which may be the reason it failed).
			failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			code := "internal"
			if _, ferr := s.store.FailQueryExecution(failCtx, db.FailQueryExecutionParams{ID: execution.ID, ErrorCode: &code}); ferr != nil && !errors.Is(ferr, pgx.ErrNoRows) {
				s.logger.ErrorContext(ctx, "failed to mark execution failed after engine lookup error", "execution_id", executionID, "lookup_err", err, "mark_err", ferr)
			}
			return db.QueryExecution{}, err
		}
		trinoResourceGroups = eng.TrinoResourceGroupsEnabled
	}
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
		workflows.QueryExecutionWorkflowV4, workflows.QueryExecutionInput{
			ExecutionID:         executionID,
			WorkspaceID:         uuid.UUID(r.WorkspaceID.Bytes).String(),
			SQL:                 r.SQL,
			Target:              r.Target,
			Identity:            r.Identity,
			StatementTimeout:    s.cfg.StatementTimeout,
			RequestID:           r.RequestID,
			ExecutionTimeout:    s.cfg.ExecutionTimeout,
			RerunExecutionID:    rerunOf,
			RerunFromPosition:   r.RerunFromPosition,
			Dialect:             dialectName,
			Class:               class,
			WorkloadEnabled:     s.cfg.WorkloadEnabled,
			TrinoResourceGroups: trinoResourceGroups,
			FairnessKey:         fairnessKey(r),
			Weight:              weight,
			AgentMaxConcurrent:  r.AgentMaxConcurrent,
			RuntimeTimeout:      runtimeTimeout,
		})
	if err != nil {
		// Detached from the request, which may be the reason it failed.
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, ferr := s.store.UpdateQueryExecution(failCtx, db.UpdateQueryExecutionParams{ID: execution.ID, Status: "failed"}); ferr != nil && !errors.Is(ferr, pgx.ErrNoRows) {
			s.logger.ErrorContext(ctx, "failed to mark execution failed after workflow start error",
				"execution_id", executionID, "start_err", err, "mark_err", ferr)
		}
		return db.QueryExecution{}, err
	}
	return execution, nil
}

// WorkflowID is the id of an execution's workflow.
func WorkflowID(executionID pgtype.UUID) string {
	return workflows.ExecutionWorkflowID(uuid.UUID(executionID.Bytes).String())
}

// fairnessKey shares a class's slots across workspaces: the agent for an agent
// run, the workspace otherwise (#399).
func fairnessKey(r Run) string {
	if r.AgentID.Valid {
		return uuid.UUID(r.AgentID.Bytes).String()
	}
	return uuid.UUID(r.WorkspaceID.Bytes).String()
}

// ruleInput is the run as the rule matcher sees it (#546).
func (r Run) ruleInput(statements int) workload.RuleInput {
	return workload.RuleInput{
		WorkspaceID:  uuidString(r.WorkspaceID),
		TeamID:       uuidString(r.TeamID),
		Role:         r.Role,
		UserID:       uuidString(r.UserID),
		AgentID:      uuidString(r.AgentID),
		EngineID:     uuidString(r.Target.EngineID),
		Origin:       r.Origin,
		SavedQueryID: uuidString(r.SavedQueryID),
		Statements:   statements,
	}
}

// costRoutingTimeout bounds the EXPLAIN; an estimate is a hint, never worth
// delaying a run (#550).
const costRoutingTimeout = 3 * time.Second

// costHeavy asks Trino for an EXPLAIN (FORMAT JSON) and reports whether the
// estimate is above the engine's threshold (#550). It is best effort: an
// engine without cost routing, a non-SELECT, a failed EXPLAIN or a plan with
// no statistics falls through to the next classification step.
// costHeavy asks Trino for an EXPLAIN (FORMAT JSON) of every SELECT in the
// script and reports whether any estimate is above the engine's threshold
// (#550). It is best effort: an engine without cost routing, a non-SELECT
// statement, a failed EXPLAIN or a plan with no statistics falls through to
// the next classification step. Rules are evaluated before this call, so a
// matching rule never pays for the estimate (#591).
func (s *Service) costHeavy(ctx context.Context, r Run, statements []string) bool {
	if s.engines == nil || !r.Target.EngineID.Valid || r.Target.EngineKind != "trino" {
		return false
	}
	row, err := s.store.GetEngineByID(ctx, r.Target.EngineID)
	if err != nil || !row.CostRoutingEnabled || row.CostThresholdBytes == nil {
		return false
	}
	engine, err := s.engines.Get(ctx, r.Target.EngineID)
	if err != nil {
		s.logger.WarnContext(ctx, "cost routing: engine lookup failed; classifying without it", "engine_id", uuidString(r.Target.EngineID), "err", err)
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, costRoutingTimeout)
	defer cancel()
	spec := queryengine.SessionSpec{
		Catalog:  r.Target.EngineCatalog,
		Identity: r.Identity,
		ReadOnly: true,
		Timeout:  costRoutingTimeout,
		Tags:     queryengine.Tags{RequestID: r.RequestID, Source: "classification", Class: queryengine.ClassInteractive},
	}
	if r.Target.Schema != "" {
		spec.Schema = []string{r.Target.Schema}
	}
	for _, sql := range statements {
		if !workload.IsSelect(sql) {
			continue
		}
		table, err := queryengine.Collect(cctx, engine, spec, "EXPLAIN (FORMAT JSON) "+sql, 1)
		if err != nil {
			s.logger.DebugContext(ctx, "cost routing: EXPLAIN failed; classifying without it", "engine_id", uuidString(r.Target.EngineID), "err", err)
			continue
		}
		costly := false
		if table.NumRows() > 0 && table.NumCols() > 0 {
			if bytes, ok := workload.TrinoPlanCost([]byte(table.Column(0).Data().Chunk(0).ValueStr(0))); ok && bytes > *row.CostThresholdBytes {
				costly = true
			}
		}
		table.Release()
		if costly {
			return true
		}
	}
	return false
}

// splitStatements is the run's statements, split with the target's dialect
// (Trino when the kind has none). It feeds statement-count rules and the DDL
// and cost steps (#546, #550, #580). An unsplittable script yields nil.
func (s *Service) splitStatements(r Run) []string {
	if s.dialects == nil {
		return nil
	}
	d, ok := s.dialects.ForEngineKind(r.Target.EngineKind)
	if !ok {
		d, ok = s.dialects.ForEngineKind("trino")
		if !ok {
			return nil
		}
	}
	parts, err := d.SplitStatements(r.SQL)
	if err != nil {
		return nil
	}
	return parts
}

// workloadRules loads the ordered rules for classification (#546). A rule that
// cannot be decoded is skipped, never allowed to break a run.
func (s *Service) workloadRules(ctx context.Context) []workload.Rule {
	rows, err := s.store.ListWorkloadRules(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "could not load workload rules; classifying without them", "err", err)
		return nil
	}
	rules := make([]workload.Rule, 0, len(rows))
	for _, row := range rows {
		match, err := workload.ParseRuleMatch(row.Match)
		if err != nil {
			s.logger.WarnContext(ctx, "skipping an invalid workload rule", "rule_id", uuidString(row.ID), "err", err)
			continue
		}
		rule := workload.Rule{
			ID:       uuidString(row.ID),
			Position: int(row.Position),
			Enabled:  row.Enabled,
			Match:    match,
			Class:    queryengine.WorkloadClass(row.Class),
			Weight:   int(row.Weight),
		}
		if row.MaxClass != nil {
			rule.MaxClass = queryengine.WorkloadClass(*row.MaxClass)
		}
		rules = append(rules, rule)
	}
	return rules
}

// uuidString renders a UUID, or "" when it is not set.
func uuidString(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}

// ensureAdmissionWorkflow starts the engine's entity workflow with the
// engine's own capacities; already running is the expected answer and ignored.
// It returns the resolved per-class capacities, so the caller can pick the
// run's runtime bound (#578).
func (s *Service) ensureAdmissionWorkflow(ctx context.Context, target queryengine.Target) map[queryengine.WorkloadClass]workload.Capacity {
	engineID := uuid.UUID(target.EngineID.Bytes).String()
	state, resolved := s.capacitiesFor(ctx, target.EngineID, engineClassOf(target))
	_, err := s.workflows.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: workflows.AdmissionWorkflowID(engineID), TaskQueue: TaskQueue},
		workflows.AdmissionWorkflow, engineID, state)
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		s.logger.WarnContext(ctx, "could not start the admission workflow", "engine_id", engineID, "err", err)
	}
	return resolved
}

// engineClassOf maps an engine kind to the capacity model's engine class.
func engineClassOf(target queryengine.Target) string {
	if target.EngineKind == "trino" {
		return "federated"
	}
	return "direct"
}

// capacitiesFor is the admission workflow's initial state and the resolved
// per-class capacities: the engine's own over the defaults, plus its optional
// ceiling.
func (s *Service) capacitiesFor(ctx context.Context, engineID pgtype.UUID, engineClass string) (workflows.AdmissionState, map[queryengine.WorkloadClass]workload.Capacity) {
	state := workflows.AdmissionState{Capacities: workflows.DefaultAdmissionCapacitiesFor(engineClass)}

	configured := map[queryengine.WorkloadClass]*workload.Capacity{}
	if rows, err := s.store.ListEngineQueueCapacities(ctx, engineID); err == nil {
		for _, row := range rows {
			capacity := &workload.Capacity{
				MaxRunning:   int(row.MaxRunning),
				MaxQueued:    int(row.MaxQueued),
				QueueTimeout: time.Duration(row.QueueTimeoutMs) * time.Millisecond,
			}
			if row.RuntimeTimeoutMs != nil {
				capacity.RuntimeTimeout = time.Duration(*row.RuntimeTimeoutMs) * time.Millisecond
			}
			configured[queryengine.WorkloadClass(row.Class)] = capacity
		}
	}
	classes := []queryengine.WorkloadClass{
		queryengine.ClassInteractive,
		queryengine.ClassHeavy,
		queryengine.ClassBackground,
	}
	out := make([]workflows.AdmissionCapacity, 0, len(classes))
	resolved := make(map[queryengine.WorkloadClass]workload.Capacity, len(classes))
	for _, class := range classes {
		capacity := workload.Resolve(engineClass, class, configured[class])
		resolved[class] = capacity
		out = append(out, workflows.AdmissionCapacity{
			Class:        class,
			MaxRunning:   capacity.MaxRunning,
			MaxQueued:    capacity.MaxQueued,
			QueueTimeout: capacity.QueueTimeout,
		})
	}
	state.Capacities = out

	if eng, err := s.store.GetEngineByID(ctx, engineID); err == nil && eng.MaxRunningTotal != nil {
		state.MaxRunningTotal = int(*eng.MaxRunningTotal)
	}
	return state, resolved
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
			s.logger.WarnContext(ctx, "failed to cancel temporal workflow", "execution_id", id.String(), "err", cancelErr)
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
		s.logger.WarnContext(ctx, "cancel on engine: no engine", "execution_id", e.ID.String(), "err", err)
		return
	}
	for _, r := range rows {
		var handle queryengine.QueryHandle
		if json.Unmarshal(r.QueryHandle, &handle) != nil {
			continue
		}
		if err := eng.Cancel(ctx, handle); err != nil {
			s.logger.WarnContext(ctx, "cancel on engine failed", "execution_id", e.ID.String(), "position", r.Position, "err", err)
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
