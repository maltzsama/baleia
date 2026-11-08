package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/jackc/pgx/v5/pgtype"
)

// workbenchStore is the query_editor and statement_refs persistence the
// activities need. *db.Queries implements it; *repo.Repo implements it on the
// GORM store (#770).
type workbenchStore interface {
	GetQueryExecutionByID(ctx context.Context, id pgtype.UUID) (db.QueryExecution, error)
	UpdateQueryExecution(ctx context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error)
	FailQueryExecution(ctx context.Context, arg db.FailQueryExecutionParams) (db.QueryExecution, error)
	QueueQueryExecution(ctx context.Context, id pgtype.UUID) (db.QueryExecution, error)
	AdmitQueryExecution(ctx context.Context, id pgtype.UUID) (db.QueryExecution, error)
	SetExecutionResult(ctx context.Context, arg db.SetExecutionResultParams) error
	ListQueryStatements(ctx context.Context, executionID pgtype.UUID) ([]db.QueryStatement, error)
	CreateQueryStatement(ctx context.Context, arg db.CreateQueryStatementParams) (db.QueryStatement, error)
	UpdateQueryStatement(ctx context.Context, arg db.UpdateQueryStatementParams) (db.QueryStatement, error)
	StartQueryStatement(ctx context.Context, arg db.StartQueryStatementParams) (int64, error)
	SkipPendingStatements(ctx context.Context, executionID pgtype.UUID) error
	AbandonRunningStatements(ctx context.Context, arg db.AbandonRunningStatementsParams) ([]db.AbandonRunningStatementsRow, error)
	SetStatementQueryHandle(ctx context.Context, arg db.SetStatementQueryHandleParams) error
	ListRunningStatementHandles(ctx context.Context, executionID pgtype.UUID) ([]db.ListRunningStatementHandlesRow, error)
	GetStatementReferenceTarget(ctx context.Context, arg db.GetStatementReferenceTargetParams) (db.GetStatementReferenceTargetRow, error)
}

// workbench returns the GORM store when it is configured, sqlc otherwise.
func (a *Activities) workbench() workbenchStore {
	if a.store != nil {
		return a.store
	}
	return a.queries
}

// SetStore wires the GORM store for the domains migrated off sqlc (#770).
func (a *Activities) SetStore(store *repo.Repo) { a.store = store }
