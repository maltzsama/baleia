package api

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

// ── Folders ───────────────────────────────────────────────────────────────────

func (h *Handlers) ListFolders(ctx context.Context, request ListFoldersRequestObject) (ListFoldersResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListFolders401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var parentID pgtype.UUID
	if request.Params.ParentId != nil {
		parentID.Bytes = *request.Params.ParentId
		parentID.Valid = true
	}

	folders, err := h.queries.ListQueryFolders(ctx, db.ListQueryFoldersParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ParentID:    parentID,
	})
	if err != nil {
		return nil, err
	}

	result := make([]QueryFolder, 0, len(folders))
	for _, f := range folders {
		result = append(result, dbFolderToPublic(f))
	}
	return ListFolders200JSONResponse(result), nil
}

func (h *Handlers) CreateFolder(ctx context.Context, request CreateFolderRequestObject) (CreateFolderResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CreateFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var parentID pgtype.UUID
	if request.Body.ParentId != nil {
		parentID.Bytes = *request.Body.ParentId
		parentID.Valid = true
	}

	folder, err := h.queries.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ParentID:    parentID,
		Name:        request.Body.Name,
	})
	if err != nil {
		return nil, err
	}

	return CreateFolder201JSONResponse(dbFolderToPublic(folder)), nil
}

func (h *Handlers) DeleteFolder(ctx context.Context, request DeleteFolderRequestObject) (DeleteFolderResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return DeleteFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var folderID pgtype.UUID
	folderID.Bytes = request.FolderId
	folderID.Valid = true

	if err := h.queries.DeleteQueryFolder(ctx, db.DeleteQueryFolderParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          folderID,
	}); err != nil {
		return DeleteFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
	}

	return DeleteFolder204Response{}, nil
}

// ── Saved Queries ─────────────────────────────────────────────────────────────

func (h *Handlers) ListSavedQueries(ctx context.Context, request ListSavedQueriesRequestObject) (ListSavedQueriesResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListSavedQueries401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var folderID pgtype.UUID
	if request.Params.FolderId != nil {
		folderID.Bytes = *request.Params.FolderId
		folderID.Valid = true
	}

	queries, err := h.queries.ListSavedQueries(ctx, db.ListSavedQueriesParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		FolderID:    folderID,
	})
	if err != nil {
		return nil, err
	}

	result := make([]SavedQuery, 0, len(queries))
	for _, q := range queries {
		result = append(result, dbSavedQueryToPublic(q))
	}
	return ListSavedQueries200JSONResponse(result), nil
}

func (h *Handlers) CreateSavedQuery(ctx context.Context, request CreateSavedQueryRequestObject) (CreateSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CreateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	createdBy, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	var folderID pgtype.UUID
	if request.Body.FolderId != nil {
		folderID.Bytes = *request.Body.FolderId
		folderID.Valid = true
	}

	query, err := h.queries.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		FolderID:    folderID,
		CreatedBy:   createdBy,
		Name:        request.Body.Name,
		Sql:         request.Body.Sql,
	})
	if err != nil {
		return nil, err
	}

	return CreateSavedQuery201JSONResponse(dbSavedQueryToPublic(query)), nil
}

func (h *Handlers) GetSavedQuery(ctx context.Context, request GetSavedQueryRequestObject) (GetSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var queryID pgtype.UUID
	queryID.Bytes = request.QueryId
	queryID.Valid = true

	query, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          queryID,
	})
	if err != nil {
		return GetSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	return GetSavedQuery200JSONResponse(dbSavedQueryToPublic(query)), nil
}

func (h *Handlers) UpdateSavedQuery(ctx context.Context, request UpdateSavedQueryRequestObject) (UpdateSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return UpdateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var queryID pgtype.UUID
	queryID.Bytes = request.QueryId
	queryID.Valid = true

	current, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          queryID,
	})
	if err != nil {
		return UpdateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	name := current.Name
	sql := current.Sql
	var folderID pgtype.UUID
	if current.FolderID.Valid {
		folderID = current.FolderID
	}

	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Sql != nil {
		sql = *request.Body.Sql
	}
	if request.Body.FolderId != nil {
		folderID.Bytes = *request.Body.FolderId
		folderID.Valid = true
	}

	updated, err := h.queries.UpdateSavedQuery(ctx, db.UpdateSavedQueryParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          queryID,
		Name:        name,
		Sql:         sql,
		FolderID:    folderID,
	})
	if err != nil {
		return nil, err
	}

	return UpdateSavedQuery200JSONResponse(dbSavedQueryToPublic(updated)), nil
}

func (h *Handlers) DeleteSavedQuery(ctx context.Context, request DeleteSavedQueryRequestObject) (DeleteSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return DeleteSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var queryID pgtype.UUID
	queryID.Bytes = request.QueryId
	queryID.Valid = true

	if err := h.queries.DeleteSavedQuery(ctx, db.DeleteSavedQueryParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          queryID,
	}); err != nil {
		return DeleteSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	return DeleteSavedQuery204Response{}, nil
}

// ── Executions ────────────────────────────────────────────────────────────────

func (h *Handlers) ListExecutions(ctx context.Context, request ListExecutionsRequestObject) (ListExecutionsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListExecutions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	page := 1
	perPage := 20
	if request.Params.Page != nil {
		page = *request.Params.Page
	}
	if request.Params.PerPage != nil {
		perPage = *request.Params.PerPage
	}

	offset := (page - 1) * perPage

	executions, err := h.queries.ListQueryExecutionsByWorkspace(ctx, db.ListQueryExecutionsByWorkspaceParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		Limit:       int32(perPage),
		Offset:      int32(offset),
	})
	if err != nil {
		return nil, err
	}

	total := len(executions)
	totalPages := int(math.Ceil(float64(total) / float64(perPage)))
	if totalPages < 1 {
		totalPages = 1
	}

	result := make([]Execution, 0, len(executions))
	for _, e := range executions {
		result = append(result, dbExecutionToPublic(e))
	}

	return ListExecutions200JSONResponse{
		Data: &result,
		Pagination: &Pagination{
			Page:       page,
			PerPage:    perPage,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

func (h *Handlers) RunQuery(ctx context.Context, request RunQueryRequestObject) (RunQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return RunQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	var wsID pgtype.UUID
	wsID.Bytes = request.Id
	wsID.Valid = true

	workspace, err := h.queries.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return RunQuery422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "workspace not found",
			},
		}, nil
	}

	var savedQueryID pgtype.UUID
	if request.Body.SavedQueryId != nil {
		savedQueryID.Bytes = *request.Body.SavedQueryId
		savedQueryID.Valid = true
	}

	execution, err := h.queries.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID:  workspaceIDFromRequest(request.Id),
		UserID:       uid,
		SavedQueryID: savedQueryID,
		Sql:          request.Body.Sql,
		TrinoCatalog: &workspace.PolarisCatalogName,
	})
	if err != nil {
		return nil, err
	}

	executionID := openapi_types.UUID(execution.ID.Bytes).String()

	wo := client.StartWorkflowOptions{
		ID:        "exec-" + executionID,
		TaskQueue: "baleia-task-queue",
	}

	_, err = h.temporalClient.ExecuteWorkflow(ctx, wo, workflows.QueryExecutionWorkflow,
		executionID,
		request.Id.String(),
		request.Body.Sql,
		workspace.PolarisCatalogName,
	)
	if err != nil {
		return nil, err
	}

	return RunQuery202JSONResponse(dbExecutionToPublic(execution)), nil
}

func (h *Handlers) GetExecution(ctx context.Context, request GetExecutionRequestObject) (GetExecutionResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	execution, err := h.queries.GetQueryExecutionByID(ctx, executionID)
	if err != nil {
		return GetExecution404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	return GetExecution200JSONResponse(dbExecutionToPublic(execution)), nil
}

func (h *Handlers) CancelExecution(ctx context.Context, request CancelExecutionRequestObject) (CancelExecutionResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CancelExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	execution, err := h.queries.GetQueryExecutionByID(ctx, executionID)
	if err != nil {
		return CancelExecution404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	if execution.Status != "running" {
		return CancelExecution200JSONResponse(dbExecutionToPublic(execution)), nil
	}

	updated, err := h.queries.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID:     executionID,
		Status: "cancelled",
	})
	if err != nil {
		return nil, err
	}

	return CancelExecution200JSONResponse(dbExecutionToPublic(updated)), nil
}

func (h *Handlers) ListStatements(ctx context.Context, request ListStatementsRequestObject) (ListStatementsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListStatements401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	statements, err := h.queries.ListQueryStatements(ctx, executionID)
	if err != nil {
		return nil, err
	}

	result := make([]QueryStatement, 0, len(statements))
	for _, s := range statements {
		result = append(result, dbStatementToPublic(s))
	}

	return ListStatements200JSONResponse(result), nil
}

func (h *Handlers) GetResults(ctx context.Context, request GetResultsRequestObject) (GetResultsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetResults401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	execution, err := h.queries.GetQueryExecutionByID(ctx, executionID)
	if err != nil {
		return GetResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	if execution.Status != "finished" {
		return GetResults409JSONResponse{
			Code:    "conflict",
			Message: "execution not yet completed",
		}, nil
	}

	if execution.ResultKey == nil || *execution.ResultKey == "" {
		return GetResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "no results available"}}, nil
	}

	presignedURL, err := h.s3.PresignedGetURL(ctx, *execution.ResultKey, 1*time.Hour)
	if err != nil {
		return nil, err
	}

	return resultsRedirectResponse{url: presignedURL}, nil
}

type resultsRedirectResponse struct {
	url string
}

func (r resultsRedirectResponse) VisitGetResultsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"url":"` + r.url + `"}`))
	return nil
}

func (h *Handlers) StreamExecution(ctx context.Context, request StreamExecutionRequestObject) (StreamExecutionResponseObject, error) {
	return StreamExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "websocket not yet implemented"}}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbFolderToPublic(f db.QueryFolder) QueryFolder {
	id := openapi_types.UUID(f.ID.Bytes)
	workspaceID := openapi_types.UUID(f.WorkspaceID.Bytes)
	qf := QueryFolder{
		Id:          id,
		WorkspaceId: workspaceID,
		Name:        f.Name,
		CreatedAt:   pgTimestampToTime(f.CreatedAt),
	}
	if f.ParentID.Valid {
		pid := openapi_types.UUID(f.ParentID.Bytes)
		qf.ParentId = &pid
	}
	return qf
}

func dbSavedQueryToPublic(q db.SavedQuery) SavedQuery {
	id := openapi_types.UUID(q.ID.Bytes)
	workspaceID := openapi_types.UUID(q.WorkspaceID.Bytes)
	createdBy := openapi_types.UUID(q.CreatedBy.Bytes)
	sq := SavedQuery{
		Id:          id,
		WorkspaceId: workspaceID,
		CreatedBy:   createdBy,
		Name:        q.Name,
		Sql:         q.Sql,
		CreatedAt:   pgTimestampToTime(q.CreatedAt),
	}
	if q.FolderID.Valid {
		fid := openapi_types.UUID(q.FolderID.Bytes)
		sq.FolderId = &fid
	}
	return sq
}

func dbExecutionToPublic(e db.QueryExecution) Execution {
	id := openapi_types.UUID(e.ID.Bytes)
	workspaceID := openapi_types.UUID(e.WorkspaceID.Bytes)
	status := ExecutionStatus(e.Status)

	exec := Execution{
		Id:          id,
		WorkspaceId: workspaceID,
		Status:      status,
		Sql:         e.Sql,
	}
	if e.SavedQueryID.Valid {
		sqid := openapi_types.UUID(e.SavedQueryID.Bytes)
		exec.SavedQueryId = &sqid
	}
	if e.TrinoQueryID != nil {
		exec.TrinoQueryId = e.TrinoQueryID
	}
	if e.RowsReturned != nil {
		rows := int(*e.RowsReturned)
		exec.RowsReturned = &rows
	}
	if e.DurationMs != nil {
		dur := int(*e.DurationMs)
		exec.DurationMs = &dur
	}
	return exec
}

func dbStatementToPublic(s db.QueryStatement) QueryStatement {
	id := openapi_types.UUID(s.ID.Bytes)
	execID := openapi_types.UUID(s.ExecutionID.Bytes)

	qs := QueryStatement{
		Id:          id,
		ExecutionId: execID,
		Position:    int(s.Position),
		Sql:         s.Sql,
		Status:      QueryStatementStatus(s.Status),
	}
	if s.TrinoQueryID != nil {
		qs.TrinoQueryId = s.TrinoQueryID
	}
	if s.RowsReturned != nil {
		rows := int(*s.RowsReturned)
		qs.RowsReturned = &rows
	}
	if s.DurationMs != nil {
		dur := int(*s.DurationMs)
		qs.DurationMs = &dur
	}
	if s.ErrorMessage != nil {
		qs.ErrorMessage = s.ErrorMessage
	}
	return qs
}
