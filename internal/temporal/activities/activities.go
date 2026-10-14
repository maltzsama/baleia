package activities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
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
	ExecuteStream(ctx context.Context, sql string, opts trino.ExecuteOptions,
		onPage func(cols []trino.Column, rows [][]interface{}) error) (string, error)
	Execute(ctx context.Context, sql, catalog, schema string) (*trino.QueryResult, error)
	KillQuery(ctx context.Context, queryID, message string) error
	ListQueries(ctx context.Context) ([]trino.QueryInfo, error)
	GetClusterStats(ctx context.Context) (*trino.ClusterStats, error)
}

type Activities struct {
	queries db.Querier
	trino   trinoClient
	s3      s3Client
	cfg     *config.Config
	// keyring opens stored connection credentials (table health talks to the
	// catalog manager). May be nil on a fresh install with no credentials.
	keyring *crypto.Keyring
	// connections reads metadata from catalog managers (table health).
	connections *connections.Registry
}

// s3Client is the slice of the storage client result streaming needs. It is an
// interface so tests can inject a fake.
type s3Client interface {
	PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
}

func New(pool *pgxpool.Pool, cfg *config.Config, keyring *crypto.Keyring, conns *connections.Registry) *Activities {
	a := &Activities{
		queries: db.New(pool),
		// The worker's client runs user queries. Its configured user is the
		// service fallback; per-statement identity is set via ExecuteOptions.
		trino:   trino.New(cfg.TrinoURL, cfg.TrinoServiceUser),
		s3:      storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL),
		cfg:         cfg,
		keyring:     keyring,
		connections: conns,
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
	w.RegisterActivity(SetExecutionResult)
	w.RegisterActivity(LensPollTrinoQueries)
	w.RegisterActivity(LensPollTrinoCluster)
	w.RegisterActivity(ListEnabledIcebergCatalogs)
	w.RegisterActivity(LensAnalyzeTableHealth)
	w.RegisterActivity(LensRunMaintenance)
	w.RegisterActivity(RetrieverRefresh)
	w.RegisterActivity(RetrieverDispatch)
	w.RegisterActivity(AdvanceRetrieverSchedule)
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
	// statement, before the workflow starts any statement. The editor gate also
	// rejects EXECUTE IMMEDIATE smuggling catalog DDL and SET SESSION
	// AUTHORIZATION.
	for _, stmt := range stmts {
		if sqlsplitter.IsForbiddenInEditor(stmt) {
			return nil, fmt.Errorf("catalog management and session authorization are not allowed in the SQL editor")
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
	// User is the per-statement Trino identity (the mapped Baleia user), empty
	// to fall back to the service user. Never the admin identity.
	User string
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

// arrowContentType is the MIME type used for stored Arrow IPC streams.
const arrowContentType = "application/vnd.apache.arrow.stream"

// errResultCapReached signals that a statement hit the row/byte cap. It is an
// internal sentinel, converted to a truncated (not failed) result.
var errResultCapReached = errors.New("statement result cap reached")

// ExecuteStatementResult is the control-plane summary of one statement. Result
// rows are never part of it; they live in object storage under ResultKey.
type ExecuteStatementResult struct {
	TrinoQueryID string
	RowCount     int64
	DurationMs   int64
	Columns      []ColumnResult
	ResultKey    string // "" when the statement returns no result set (DDL/DML)
	ResultBytes  int64
	Truncated    bool
	Error        string
}

type ColumnResult struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func ExecuteStatement(ctx context.Context, params ExecuteStatementParams) (*ExecuteStatementResult, error) {
	return defaultActivities.executeStatement(ctx, params)
}

// countingWriter counts bytes while proxying to the underlying pipe.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// s3ResultKey lays out result objects per statement so future result sets
// (result_index > 0) fit the same layout.
func s3ResultKey(executionID string, position, resultIndex int) string {
	return fmt.Sprintf("query-results/%s/%d/%d.arrow", executionID, position, resultIndex)
}

func (a *Activities) executeStatement(ctx context.Context, params ExecuteStatementParams) (*ExecuteStatementResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("executing statement", "execution_id", params.ExecutionID, "request_id", params.RequestID, "position", params.Position)

	// I5 (CR-017): defense in depth. splitSQL already rejects catalog DDL and
	// forbidden editor statements, but ensure that any path that bypasses
	// splitting (single-statement fast path, future callers) still cannot issue
	// them.
	if sqlsplitter.IsForbiddenInEditor(params.SQL) {
		return &ExecuteStatementResult{
			Error: "catalog management and session authorization are not allowed in the SQL editor",
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
		User:              params.User,
		SessionProperties: statementSessionProperties(params.Timeout),
		OnQueryID: func(id string) {
			queryID = id
			beat(true)
		},
		OnProgress: func() { beat(false) },
	}

	// Streaming pipeline: Trino page -> typed Arrow builders -> io.Pipe ->
	// S3 multipart upload. One Arrow object per statement. The stream is
	// created lazily on the first page that carries columns, so DDL/DML
	// statements (no schema) upload nothing.
	var columns []ColumnResult
	var sw *arrowipc.StreamWriter
	var pw *io.PipeWriter
	var pr *io.PipeReader
	var cw *countingWriter
	var resultKey string
	var uploadedBytes int64
	var uploadErr error
	var uploadDone chan struct{}
	var rowTotal int64
	var truncated bool

	onPage := func(cols []trino.Column, rows [][]interface{}) error {
		if len(cols) > 0 && columns == nil {
			columns = make([]ColumnResult, len(cols))
			aCols := make([]arrowipc.ColumnInfo, len(cols))
			for i, c := range cols {
				columns[i] = ColumnResult{Name: c.Name, Type: c.Type}
				aCols[i] = arrowipc.ColumnInfo{Name: c.Name, Type: c.Type}
			}

			resultKey = s3ResultKey(params.ExecutionID, int(params.Position), 0)
			pr, pw = io.Pipe()
			cw = &countingWriter{w: pw}
			s, err := arrowipc.NewStreamWriter(cw, aCols)
			if err != nil {
				_ = pw.Close()
				_ = pr.Close()
				return err
			}
			sw = s
			uploadDone = make(chan struct{})
			go func() {
				defer close(uploadDone)
				n, err := a.s3.PutStream(ctx, resultKey, pr, arrowContentType)
				if err != nil {
					uploadErr = err
					return
				}
				uploadedBytes = n
			}()
		}

		if len(rows) == 0 {
			return nil
		}
		if sw == nil {
			return errors.New("statement returned rows without a result schema")
		}

		for _, row := range rows {
			if rowTotal >= a.cfg.ResultMaxRows || (cw != nil && cw.n >= a.cfg.ResultMaxBytes) {
				return errResultCapReached
			}
			if err := sw.AppendRows([][]interface{}{row}); err != nil {
				return err
			}
			rowTotal++
			if cw != nil && cw.n >= a.cfg.ResultMaxBytes {
				return errResultCapReached
			}
		}
		return nil
	}

	// A statement is never retried once Trino has accepted it: retrying an
	// INSERT/CREATE could write twice. Only a transport failure before the
	// query id is known is retried, and with a bounded backoff.
	backoff := statementTransportBackoff
	var err error
	for attempt := 0; ; attempt++ {
		queryID = ""
		_, err = a.trino.ExecuteStream(ctx, params.SQL, opts, onPage)
		if err == nil || ctx.Err() != nil || queryID != "" || attempt >= len(backoff) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff[attempt]):
		}
	}

	durationMs := time.Since(start).Milliseconds()

	// On cancellation finalize the pipe first so the upload goroutine sees EOF
	// and cannot deadlock, then abandon the query on Trino.
	if ctx.Err() != nil {
		if pw != nil {
			_ = pw.Close()
		}
		if uploadDone != nil {
			<-uploadDone
		}
		if queryID != "" {
			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = a.trino.KillQuery(killCtx, queryID, "cancelled by baleia")
			killCancel()
		}
		return nil, temporal.NewCanceledError()
	}

	if errors.Is(err, errResultCapReached) {
		if queryID != "" {
			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = a.trino.KillQuery(killCtx, queryID, "statement result cap reached")
			killCancel()
		}
		truncated = true
		err = nil
	}

	if sw != nil {
		if cerr := sw.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if pw != nil {
		_ = pw.Close()
	}
	if uploadDone != nil {
		<-uploadDone
	}

	if err != nil {
		return &ExecuteStatementResult{
			TrinoQueryID: queryID,
			DurationMs:   durationMs,
			Error:        err.Error(),
		}, nil
	}
	if uploadErr != nil {
		return &ExecuteStatementResult{
			TrinoQueryID: queryID,
			RowCount:     rowTotal,
			DurationMs:   durationMs,
			Error:        fmt.Sprintf("store results: %v", uploadErr),
		}, nil
	}

	res := &ExecuteStatementResult{
		TrinoQueryID: queryID,
		RowCount:     rowTotal,
		DurationMs:   durationMs,
		Columns:      columns,
		Truncated:    truncated,
	}
	if sw != nil {
		res.ResultKey = resultKey
		res.ResultBytes = uploadedBytes
	}
	return res, nil
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
	ResultKey    string
	ResultBytes  int64
	Truncated    bool
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
		if s.Position != params.Position {
			continue
		}

		var errMsg *string
		if params.ErrorMessage != "" {
			errMsg = &params.ErrorMessage
		}
		var trinoID *string
		if params.TrinoQueryID != "" {
			trinoID = &params.TrinoQueryID
		}
		var resultKey *string
		if params.ResultKey != "" {
			resultKey = &params.ResultKey
		}
		var finishedAt pgtype.Timestamptz
		if params.Status == "finished" || params.Status == "failed" || params.Status == "cancelled" {
			finishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}

		_, err := a.queries.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{
			ID:           s.ID,
			Status:       params.Status,
			TrinoQueryID: trinoID,
			RowsReturned: &params.RowsReturned,
			DurationMs:   &params.DurationMs,
			ErrorMessage: errMsg,
			FinishedAt:   finishedAt,
			ResultKey:    resultKey,
			ResultBytes:  &params.ResultBytes,
			Truncated:    params.Truncated,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The statement was already terminal (cancelled); a late update
			// must not overwrite it.
			return nil
		}
		if err != nil {
			return err
		}

		// The execution's result_key points at the last statement that
		// produced a result set; runs are processed in statement order so
		// later statements naturally win.
		if resultKey != nil {
			if err := a.queries.SetExecutionResult(ctx, db.SetExecutionResultParams{
				ID:        execID,
				ResultKey: resultKey,
			}); err != nil {
				return fmt.Errorf("set execution result: %w", err)
			}
		}
		return nil
	}
	return nil
}

type SetExecutionResultParams struct {
	ExecutionID string
	ResultKey   string
}

func SetExecutionResult(ctx context.Context, params SetExecutionResultParams) error {
	return defaultActivities.setExecutionResult(ctx, params)
}

func (a *Activities) setExecutionResult(ctx context.Context, params SetExecutionResultParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(params.ExecutionID); err != nil {
		return err
	}
	return a.queries.SetExecutionResult(ctx, db.SetExecutionResultParams{
		ID:        execID,
		ResultKey: &params.ResultKey,
	})
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
