package activities

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/sqlsplitter"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

var defaultActivities *Activities

// trinoClient is the slice of the Trino client the activities use. It is an
// interface so tests can inject a fake that blocks, fails, or records calls.
type trinoClient interface {
	ExecuteWithOptions(ctx context.Context, sql string, opts trino.ExecuteOptions) (*trino.QueryResult, error)
	Execute(ctx context.Context, sql, catalog, schema string) (*trino.QueryResult, error)
	KillQuery(ctx context.Context, queryID, message string) error
	ListQueries(ctx context.Context) ([]trino.QueryInfo, error)
	GetClusterStats(ctx context.Context) (*trino.ClusterStats, error)
}

type Activities struct {
	queries db.Querier
	trino   trinoClient
	s3      *storage.S3Client
	cfg     *config.Config
}

func New(pool *pgxpool.Pool, cfg *config.Config) *Activities {
	a := &Activities{
		queries: db.New(pool),
		trino:   trino.New(cfg.TrinoURL, "baleia"),
		s3:      storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL),
		cfg:     cfg,
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
	w.RegisterActivity(CreateStatementRecords)
	w.RegisterActivity(ExecuteStatement)
	w.RegisterActivity(UpdateStatementRecord)
	w.RegisterActivity(CompleteExecution)
	w.RegisterActivity(FailExecution)
	w.RegisterActivity(StoreResults)
	w.RegisterActivity(LensPollTrinoQueries)
	w.RegisterActivity(LensPollTrinoCluster)
	w.RegisterActivity(LensAnalyzeTableHealth)
	w.RegisterActivity(LensRunMaintenance)
	w.RegisterActivity(RetrieverRefresh)
	w.RegisterActivity(RetrieverPoll)
}

type SplitSQLParams struct {
	SQL string
}

type SplitSQLResult struct {
	Statements []string
}

func SplitSQL(ctx context.Context, params SplitSQLParams) (*SplitSQLResult, error) {
	return defaultActivities.splitSQL(ctx, params)
}

func (a *Activities) splitSQL(ctx context.Context, params SplitSQLParams) (*SplitSQLResult, error) {
	stmts := sqlsplitter.Split(params.SQL)
	if len(stmts) == 0 {
		stmts = []string{params.SQL}
	}
	// I5 (CR-017): catalog management goes through the Baleia UI, never the
	// SQL editor. With dynamic catalog.management — which is global — any
	// user with query access could CREATE CATALOG pointed at arbitrary hosts
	// (exfiltration) or DROP another user's catalog. Reject it here, per
	// statement, before the workflow starts any statement.
	for _, stmt := range stmts {
		if sqlsplitter.IsCatalogDDL(stmt) {
			return nil, fmt.Errorf("catalog management is performed via the Baleia UI, not the SQL editor")
		}
	}
	return &SplitSQLResult{Statements: stmts}, nil
}

type CreateStatementRecordsParams struct {
	ExecutionID string
	Statements  []string
}

func CreateStatementRecords(ctx context.Context, params CreateStatementRecordsParams) error {
	return defaultActivities.createStatementRecords(ctx, params)
}

func (a *Activities) createStatementRecords(ctx context.Context, params CreateStatementRecordsParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return err
	}

	for i, sql := range params.Statements {
		_, err := a.queries.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
			ExecutionID: execID,
			Position:    int32(i + 1),
			Sql:         sql,
			Status:      "running",
		})
		if err != nil {
			return fmt.Errorf("create statement record %d: %w", i+1, err)
		}
	}
	return nil
}

type ExecuteStatementParams struct {
	ExecutionID  string
	Position     int32
	SQL          string
	TrinoCatalog string
	RequestID    string
	// Timeout bounds the statement. It is sent to Trino as the session
	// property query_max_execution_time so the cluster enforces it too.
	Timeout time.Duration
}

// heartbeatInterval throttles activity heartbeats; the workflow's heartbeat
// timeout is 3x this interval.
const heartbeatInterval = 10 * time.Second

// statementTransportBackoff bounds retries of transport failures that happen
// before Trino accepted a statement. It is a package var so tests can shrink it.
var statementTransportBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// heartbeatDetails travels with the heartbeat so a cancellation or a recovered
// attempt knows which Trino query to abandon.
type heartbeatDetails struct {
	TrinoQueryID string
}

type ExecuteStatementResult struct {
	TrinoQueryID string
	RowsReturned int64
	DurationMs   int64
	Columns      []ColumnResult
	Rows         [][]interface{}
	Error        string
}

type ColumnResult struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func ExecuteStatement(ctx context.Context, params ExecuteStatementParams) (*ExecuteStatementResult, error) {
	return defaultActivities.executeStatement(ctx, params)
}

func (a *Activities) executeStatement(ctx context.Context, params ExecuteStatementParams) (*ExecuteStatementResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("executing statement", "execution_id", params.ExecutionID, "request_id", params.RequestID, "position", params.Position)

	// I5 (CR-017): defense in depth. splitSQL already rejects catalog DDL,
	// but ensure that any path that bypasses splitting (single-statement
	// fast path, future callers) still cannot issue CREATE/DROP CATALOG.
	if sqlsplitter.IsCatalogDDL(params.SQL) {
		return &ExecuteStatementResult{
			Error: "catalog management is performed via the Baleia UI, not the SQL editor",
		}, nil
	}

	start := time.Now()

	// Heartbeats let Temporal deliver a cancellation to this attempt and prove
	// liveness. They are throttled to heartbeatInterval.
	var queryID string
	var lastBeat time.Time
	beat := func(force bool) {
		now := time.Now()
		if !force && now.Sub(lastBeat) < heartbeatInterval {
			return
		}
		lastBeat = now
		activity.RecordHeartbeat(ctx, heartbeatDetails{TrinoQueryID: queryID})
	}

	opts := trino.ExecuteOptions{
		Catalog:           params.TrinoCatalog,
		SessionProperties: statementSessionProperties(params.Timeout),
		OnQueryID: func(id string) {
			queryID = id
			beat(true)
		},
		OnProgress: func() { beat(false) },
	}

	// A statement is never retried once Trino has accepted it: retrying an
	// INSERT/CREATE could write twice. Only a transport failure before the
	// query id is known is retried, and with a bounded backoff.
	backoff := statementTransportBackoff
	var result *trino.QueryResult
	var err error
	for attempt := 0; ; attempt++ {
		queryID = ""
		result, err = a.trino.ExecuteWithOptions(ctx, params.SQL, opts)
		if err == nil || ctx.Err() != nil || queryID != "" || attempt >= len(backoff) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff[attempt]):
		}
	}

	durationMs := time.Since(start).Milliseconds()

	if ctx.Err() != nil {
		// The client already abandoned the query on Trino; report the cancel
		// so the workflow marks the statement and execution cancelled.
		return nil, temporal.NewCanceledError()
	}

	if err != nil {
		return &ExecuteStatementResult{
			DurationMs: durationMs,
			Error:      err.Error(),
		}, nil
	}

	cols := make([]ColumnResult, len(result.Columns))
	for i, c := range result.Columns {
		cols[i] = ColumnResult{Name: c.Name, Type: c.Type}
	}

	return &ExecuteStatementResult{
		TrinoQueryID: result.QueryID,
		RowsReturned: result.RowCount,
		DurationMs:   durationMs,
		Columns:      cols,
		Rows:         result.Data,
	}, nil
}

// statementSessionProperties renders the per-statement Trino session
// properties. Trino parses query_max_execution_time as a duration such as
// "1800s".
func statementSessionProperties(timeout time.Duration) map[string]string {
	if timeout <= 0 {
		return nil
	}
	return map[string]string{
		"query_max_execution_time": strconv.FormatInt(int64(timeout.Seconds()), 10) + "s",
	}
}

type UpdateStatementRecordParams struct {
	ExecutionID  string
	Position     int32
	Status       string
	TrinoQueryID string
	RowsReturned int64
	DurationMs   int64
	ErrorMessage string
}

func UpdateStatementRecord(ctx context.Context, params UpdateStatementRecordParams) error {
	return defaultActivities.updateStatementRecord(ctx, params)
}

func (a *Activities) updateStatementRecord(ctx context.Context, params UpdateStatementRecordParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return err
	}

	statements, err := a.queries.ListQueryStatements(ctx, execID)
	if err != nil {
		return err
	}

	for _, s := range statements {
		if s.Position == params.Position {
			var errMsg *string
			if params.ErrorMessage != "" {
				errMsg = &params.ErrorMessage
			}
			var trinoID *string
			if params.TrinoQueryID != "" {
				trinoID = &params.TrinoQueryID
			}

			_, err := a.queries.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{
				ID:           s.ID,
				Status:       params.Status,
				TrinoQueryID: trinoID,
				RowsReturned: &params.RowsReturned,
				DurationMs:   &params.DurationMs,
				ErrorMessage: errMsg,
			})
			return err
		}
	}
	return nil
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

type StoreResultsParams struct {
	ExecutionID string
	Columns     []ColumnResult
	Rows        [][]interface{}
	RequestID   string
}

type StoreResultsResult struct {
	ResultKey string
}

func StoreResults(ctx context.Context, params StoreResultsParams) (*StoreResultsResult, error) {
	return defaultActivities.storeResults(ctx, params)
}

func (a *Activities) storeResults(ctx context.Context, params StoreResultsParams) (*StoreResultsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("storeResults input", "request_id", params.RequestID, "columns", len(params.Columns), "rows", len(params.Rows))
	if len(params.Rows) > 0 {
		logger.Info("first row", "len", len(params.Rows[0]), "val", params.Rows[0])
	}
	arrowCols := make([]arrowipc.ColumnInfo, len(params.Columns))
	for i, c := range params.Columns {
		arrowCols[i] = arrowipc.ColumnInfo{Name: c.Name, Type: c.Type}
	}

	data, err := arrowipc.TrinoToArrow(arrowCols, params.Rows)
	if err != nil {
		return nil, fmt.Errorf("arrow conversion: %w", err)
	}
	logger.Info("storeResults arrow", "bytes", len(data))

	key := fmt.Sprintf("query-results/%s/result.arrow", params.ExecutionID)
	if err := a.s3.Put(ctx, key, data, "application/vnd.apache.arrow.stream"); err != nil {
		return nil, fmt.Errorf("s3 put: %w", err)
	}

	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return nil, err
	}

	if err := a.queries.SetExecutionResult(ctx, db.SetExecutionResultParams{
		ID:        execID,
		ResultKey: &key,
	}); err != nil {
		return nil, fmt.Errorf("set result key: %w", err)
	}

	return &StoreResultsResult{ResultKey: key}, nil
}
