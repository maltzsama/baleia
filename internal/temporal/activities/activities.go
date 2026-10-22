package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

var defaultActivities *Activities

// engineSource returns the engine statements run on; *queryengine.Manager
// implements it. Until executions carry their target's engine (#13 PR 3),
// everything runs on the default Trino engine.
type engineSource interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
	DefaultTrino(ctx context.Context) (queryengine.Engine, error)
}

type Activities struct {
	metrics workerMetrics
	queries db.Querier
	pool    *pgxpool.Pool
	engines engineSource
	monitor lensMonitor
	s3      s3Client
	cfg     *config.Config
	// keyring opens stored connection credentials (table health talks to the
	// catalog manager). May be nil on a fresh install with no credentials.
	keyring *crypto.Keyring
	// connections reads metadata from catalog managers (table health).
	connections *connections.Registry
	// metadata stores catalog metadata snapshots (#15).
	metadata metadataSyncer
	// observeEngine observes a federated engine's catalogs (#17).
	observeEngine engineObserver
	// audit records data operations (#45; call sites #43).
	audit audit.Recorder
	// reconcileEngine reconciles a federated engine's bindings (#37).
	reconcileEngine engineReconciler
	// sweepEphemeral drops expired connection-test catalogs (#12).
	sweepEphemeral ephemeralSweeper
	// auditExport ships audit events to OpenSearch (#50); nil when disabled.
	auditExport *auditexport.Exporter
}

// SetAuditExporter wires the audit export pass (#50).
func (a *Activities) SetAuditExporter(e *auditexport.Exporter) { a.auditExport = e }

// SetAudit wires the audit recorder (#45).
func (a *Activities) SetAudit(r audit.Recorder) { a.audit = r }

// s3Client is the slice of the storage client result streaming needs. It is an
// interface so tests can inject a fake.
type s3Client interface {
	PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
}

func New(pool *pgxpool.Pool, cfg *config.Config, keyring *crypto.Keyring, conns *connections.Registry, engines engineSource, mon lensMonitor, s3 *storage.S3Client) *Activities {
	a := &Activities{
		queries:     db.New(pool),
		pool:        pool,
		engines:     engines,
		monitor:     mon,
		s3:          s3,
		cfg:         cfg,
		keyring:     keyring,
		connections: conns,
		audit:       audit.Nop{},
	}
	defaultActivities = a
	return a
}

// (EnsureCatalog activity and listRemoteCatalogs helper were removed in
// CR-017 §I3. With the baleia-trino-catalog-store plugin in place, Trino
// loads catalogs from trino_catalog_registry at boot — there is no longer a
// "Trino lost the catalog after a restart" scenario that the activity used
// to cover. Creating a catalog row is now the write-through flow in the
// CreateCatalog handler (handlers_catalogs_write.go), which also handles
// runtime propagation via CREATE CATALOG.)

func Register(w worker.Worker, a *Activities) {
	w.RegisterActivity(SplitSQL)
	w.RegisterActivity(SchedulePassSucceeded)
	w.RegisterActivity(CreateStatementRecords)
	w.RegisterActivity(ExecuteScript)
	w.RegisterActivity(AbandonStatements)
	w.RegisterActivity(CompleteExecution)
	w.RegisterActivity(FailExecution)
	w.RegisterActivity(LensPollTrinoQueries)
	w.RegisterActivity(LensPollTrinoCluster)
	w.RegisterActivity(ListEnabledIcebergCatalogs)
	w.RegisterActivity(LensAnalyzeTableHealth)
	w.RegisterActivity(LensRunMaintenance)
	w.RegisterActivity(RetrieverRefresh)
	w.RegisterActivity(RetrieverDispatch)
	w.RegisterActivity(AdvanceRetrieverSchedule)
	w.RegisterActivity(SyncCatalogMetadata)
	w.RegisterActivity(EnsureAuditPartitions)
	w.RegisterActivity(ExportAuditEvents)
	w.RegisterActivity(ReconcileCatalogs)
	w.RegisterActivity(ReconcileDirectories)
	w.RegisterActivity(SweepEphemeralCatalogs)
	w.RegisterActivity(RefreshEngineCatalogs)
	w.RegisterActivity(ListSyncableCatalogs)
	w.RegisterActivity(CleanupExpiredTokens)
}

type SplitSQLParams struct {
	SQL string
	// Dialect splits and gates the script: the target engine's.
	Dialect string
}

// paramsDialect is the dialect an activity input names.
func paramsDialect(name string) (dialect.Dialect, error) {
	d, ok := dialect.Get(name)
	if !ok {
		return nil, temporal.NewNonRetryableApplicationError("unknown dialect "+name, "editor_rejected", nil)
	}
	return d, nil
}

type SplitSQLResult struct {
	Statements []string
}

func SplitSQL(ctx context.Context, params SplitSQLParams) (*SplitSQLResult, error) {
	return defaultActivities.splitSQL(ctx, params)
}

func (a *Activities) splitSQL(ctx context.Context, params SplitSQLParams) (*SplitSQLResult, error) {
	// I5 (CR-017): catalog management goes through the Baleia UI, never the
	// SQL editor. With dynamic catalog.management — which is global — any
	// user with query access could CREATE CATALOG pointed at arbitrary hosts
	// (exfiltration) or DROP another user's catalog. Reject it here, per
	// statement, before the workflow starts any statement. The editor gate also
	// rejects EXECUTE IMMEDIATE smuggling catalog DDL and SET SESSION
	// AUTHORIZATION. The script is split with the target engine's dialect.
	d, err := paramsDialect(params.Dialect)
	if err != nil {
		return nil, err
	}
	stmts, err := policy.EditorStatements(d, params.SQL)
	if err != nil {
		// The same script fails the same way: retrying only delays the answer.
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "editor_rejected", err)
	}
	if len(stmts) == 0 {
		stmts = []string{params.SQL}
	}
	return &SplitSQLResult{Statements: stmts}, nil
}

type CreateStatementRecordsParams struct {
	ExecutionID       string
	Statements        []string
	RerunExecutionID  string
	RerunFromPosition int32
}

// CreateStatementRecordsResult is the SQL the execution actually runs: the
// split statements, or the copied statements of a rerun source (#111).
type CreateStatementRecordsResult struct {
	Statements []string
}

func CreateStatementRecords(ctx context.Context, params CreateStatementRecordsParams) (*CreateStatementRecordsResult, error) {
	return defaultActivities.createStatementRecords(ctx, params)
}

func (a *Activities) createStatementRecords(ctx context.Context, params CreateStatementRecordsParams) (*CreateStatementRecordsResult, error) {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return nil, err
	}

	statements := params.Statements
	if params.RerunExecutionID != "" {
		var srcID pgtype.UUID
		if err := srcID.Scan(params.RerunExecutionID); err != nil {
			return nil, err
		}
		src, err := a.queries.ListQueryStatements(ctx, srcID)
		if err != nil {
			return nil, err
		}
		from := params.RerunFromPosition
		if from < 1 {
			from = 1
		}
		statements = nil
		for _, stmt := range src {
			if stmt.Position >= from {
				statements = append(statements, stmt.Sql)
			}
		}
	}

	for i, sql := range statements {
		_, err := a.queries.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
			ExecutionID: execID,
			Position:    int32(i + 1),
			Sql:         sql,
			Status:      "pending",
		})
		if err != nil {
			return nil, fmt.Errorf("create statement record %d: %w", i+1, err)
		}
	}
	return &CreateStatementRecordsResult{Statements: statements}, nil
}

// resultSet is one entry of query_statements.result_sets.
type resultSet struct {
	ResultIndex int    `json:"result_index"`
	ResultKey   string `json:"result_key"`
	Rows        int64  `json:"rows"`
	Bytes       int64  `json:"bytes"`
	Truncated   bool   `json:"truncated"`
}

// resultSets renders the stored results of a statement: today at most one,
// at result_index 0 (the layout already allows more).
func resultSets(o StatementOutcome) []byte {
	sets := []resultSet{}
	if o.ResultKey != "" {
		sets = append(sets, resultSet{ResultKey: o.ResultKey, Rows: o.RowCount, Bytes: o.ResultBytes, Truncated: o.Truncated})
	}
	out, _ := json.Marshal(sets)
	return out
}

// recordOutcome ends the running statement at o.Position. The update is
// conditional on status='running', so it never overwrites a statement that
// was already abandoned.
func (a *Activities) recordOutcome(ctx context.Context, execID pgtype.UUID, o StatementOutcome) error {
	statements, err := a.queries.ListQueryStatements(ctx, execID)
	if err != nil {
		return err
	}
	for _, s := range statements {
		if s.Position != o.Position {
			continue
		}
		_, err := a.queries.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{
			ID:            s.ID,
			Status:        o.Status,
			EngineQueryID: optional(o.EngineQueryID),
			ResultSets:    resultSets(o),
			RowsReturned:  &o.RowCount,
			DurationMs:    &o.DurationMs,
			ErrorMessage:  optional(o.Error),
			ErrorCode:     optional(o.ErrorCode),
			FinishedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ResultKey:     optional(o.ResultKey),
			ResultBytes:   &o.ResultBytes,
			Truncated:     o.Truncated,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// The execution's result_key points at the last statement that
		// produced a result set; statements run in order, so later ones win.
		if o.ResultKey != "" {
			if err := a.queries.SetExecutionResult(ctx, db.SetExecutionResultParams{
				ID:        execID,
				ResultKey: &o.ResultKey,
			}); err != nil {
				return fmt.Errorf("set execution result: %w", err)
			}
		}
		return nil
	}
	return nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type CompleteExecutionParams struct {
	ExecutionID  string
	Status       string
	RowsReturned int64
	DurationMs   int64
}

func CompleteExecution(ctx context.Context, params CompleteExecutionParams) error {
	return defaultActivities.completeExecution(ctx, params)
}

func (a *Activities) completeExecution(ctx context.Context, params CompleteExecutionParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return err
	}

	_, err := a.queries.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID:           execID,
		Status:       params.Status,
		RowsReturned: &params.RowsReturned,
		DurationMs:   &params.DurationMs,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The execution was cancelled (or already terminal); do not overwrite it.
		return nil
	}
	return err
}

type FailExecutionParams struct {
	ExecutionID  string
	ErrorMessage string
}

func FailExecution(ctx context.Context, params FailExecutionParams) error {
	return defaultActivities.failExecution(ctx, params)
}

func (a *Activities) failExecution(ctx context.Context, params FailExecutionParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return err
	}

	_, err := a.queries.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID:     execID,
		Status: "failed",
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The execution was cancelled (or already terminal); do not overwrite it.
		return nil
	}
	return err
}

// workerMetrics are the worker's producers (#87); nil-safe, zero without
// SetMetrics.
type workerMetrics struct {
	engine    *metrics.Engine
	schedules *metrics.Schedules
	metadata  *metrics.Metadata
	directory *metrics.Directory
}

// SetMetrics registers the worker's producers on reg.
func (a *Activities) SetMetrics(reg *metrics.Registry) {
	a.metrics = workerMetrics{engine: reg.Engine(), schedules: reg.Schedules(), metadata: reg.Metadata(), directory: reg.Directory()}
	if s3, ok := a.s3.(interface{ SetMetrics(*metrics.Storage) }); ok {
		s3.SetMetrics(reg.Storage())
	}
}

// passDone records a successful pass of a schedule.
func (a *Activities) passDone(schedule string, err error) {
	if a != nil && err == nil {
		a.metrics.schedules.Succeeded(schedule)
	}
}

// SchedulePassSucceededParams names the schedule whose pass completed.
type SchedulePassSucceededParams struct{ Schedule string }

// SchedulePassSucceeded records a completed pass of a scheduled workflow that
// does more than one activity (table health, retriever dispatch): the
// workflow calls it last, so the gauge never runs ahead of the pass.
func SchedulePassSucceeded(ctx context.Context, params SchedulePassSucceededParams) error {
	defaultActivities.passDone(params.Schedule, nil)
	return nil
}
