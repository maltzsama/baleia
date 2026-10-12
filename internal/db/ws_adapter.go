package db

import (
	"context"

	"github.com/galileostudio/baleia/internal/ws"
	"github.com/jackc/pgx/v5/pgtype"
)

func (q *Queries) AsWSQuerier() ws.Querier {
	return &wsQuerierAdapter{q}
}

type wsQuerierAdapter struct {
	q *Queries
}

func (a *wsQuerierAdapter) CheckWorkspaceAccess(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
	return a.q.CheckWorkspaceAccess(ctx, CheckWorkspaceAccessParams{
		ID:     arg.ID,
		UserID: arg.UserID,
	})
}

func (a *wsQuerierAdapter) GetQueryExecutionByIDAndWorkspace(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error) {
	e, err := a.q.GetQueryExecutionByIDAndWorkspace(ctx, GetQueryExecutionByIDAndWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return ws.QuerierQueryExecution{}, err
	}
	return ws.QuerierQueryExecution{
		Status:       e.Status,
		RowsReturned: e.RowsReturned,
		DurationMs:   e.DurationMs,
		ResultKey:    e.ResultKey,
	}, nil
}

func (a *wsQuerierAdapter) ListQueryStatementsByWorkspace(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
	stmts, err := a.q.ListQueryStatementsByWorkspace(ctx, ListQueryStatementsByWorkspaceParams{ExecutionID: executionID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	result := make([]ws.QuerierQueryStatement, len(stmts))
	for i, s := range stmts {
		result[i] = ws.QuerierQueryStatement{
			Position:     s.Position,
			Status:       s.Status,
			RowsReturned: s.RowsReturned,
			DurationMs:   s.DurationMs,
			ErrorMessage: s.ErrorMessage,
		}
	}
	return result, nil
}
