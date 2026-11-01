package server

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/ws"
	"github.com/jackc/pgx/v5/pgtype"
)

// wsQuerier adapts *db.Queries to the WebSocket handler's narrow interfaces.
// It lives here, not in internal/db, because the handler needs the workflow
// package and the workflow package reaches internal/db: keeping the adapter
// in db would close an import cycle.
type wsQuerier struct {
	q *db.Queries
}

func (a wsQuerier) CheckWorkspaceAccess(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
	return a.q.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{
		ID:     arg.ID,
		UserID: arg.UserID,
	})
}

func (a wsQuerier) GetQueryExecutionByIDAndWorkspace(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error) {
	e, err := a.q.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return ws.QuerierQueryExecution{}, err
	}
	return ws.QuerierQueryExecution{
		ID:             e.ID,
		WorkspaceID:    e.WorkspaceID,
		Status:         e.Status,
		Sql:            e.Sql,
		RowsReturned:   e.RowsReturned,
		DurationMs:     e.DurationMs,
		StartedAt:      e.StartedAt,
		TargetSnapshot: e.TargetSnapshot,
		EngineID:       e.EngineID,
		QueueClass:     e.QueueClass,
		QueueReason:    e.QueueReason,
		QueuedAt:       e.QueuedAt,
		AdmittedAt:     e.AdmittedAt,
		QueueWaitMs:    e.QueueWaitMs,
	}, nil
}

func (a wsQuerier) ListQueryStatementsByWorkspace(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
	stmts, err := a.q.ListQueryStatementsByWorkspace(ctx, db.ListQueryStatementsByWorkspaceParams{ExecutionID: executionID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	result := make([]ws.QuerierQueryStatement, len(stmts))
	for i, s := range stmts {
		result[i] = ws.QuerierQueryStatement{
			ID:           s.ID,
			ExecutionID:  s.ExecutionID,
			Position:     s.Position,
			Sql:          s.Sql,
			Status:       s.Status,
			RowsReturned: s.RowsReturned,
			DurationMs:   s.DurationMs,
			ErrorMessage: s.ErrorMessage,
			ErrorCode:    s.ErrorCode,
			ResultSets:   s.ResultSets,
			Truncated:    s.Truncated,
			StartedAt:    s.StartedAt,
			FinishedAt:   s.FinishedAt,
		}
	}
	return result, nil
}
