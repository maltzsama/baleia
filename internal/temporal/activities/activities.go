package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/sqlsplitter"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/trino_client"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

var defaultActivities *Activities

type Activities struct {
	queries *db.Queries
	trino   *trino_client.Client
	s3      *storage.S3Client
}

func New(pool *pgxpool.Pool, cfg *config.Config) *Activities {
	a := &Activities{
		queries: db.New(pool),
		trino:   trino_client.New(cfg.TrinoURL, "baleia"),
		s3:      storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL),
	}
	defaultActivities = a
	return a
}

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
	logger.Info("executing statement", "execution_id", params.ExecutionID, "position", params.Position)

	start := time.Now()
	result, err := a.trino.Execute(ctx, params.SQL, params.TrinoCatalog, "")
	durationMs := time.Since(start).Milliseconds()

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
	return err
}

type StoreResultsParams struct {
	ExecutionID string
	Columns     []ColumnResult
	Rows        [][]interface{}
}

type StoreResultsResult struct {
	ResultKey string
}

func StoreResults(ctx context.Context, params StoreResultsParams) (*StoreResultsResult, error) {
	return defaultActivities.storeResults(ctx, params)
}

func (a *Activities) storeResults(ctx context.Context, params StoreResultsParams) (*StoreResultsResult, error) {
	arrowCols := make([]arrowipc.ColumnInfo, len(params.Columns))
	for i, c := range params.Columns {
		arrowCols[i] = arrowipc.ColumnInfo{Name: c.Name, Type: c.Type}
	}

	data, err := arrowipc.TrinoToArrow(arrowCols, params.Rows)
	if err != nil {
		return nil, fmt.Errorf("arrow conversion: %w", err)
	}

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
