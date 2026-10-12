package api

import (
	"context"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/client"
)

func (h *Handlers) ListRetrievers(ctx context.Context, request ListRetrieversRequestObject) (ListRetrieversResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListRetrievers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	retrievers, err := h.queries.ListRetrieversByWorkspace(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}

	result := make([]Retriever, 0, len(retrievers))
	for _, r := range retrievers {
		result = append(result, dbRetrieverToPublic(r))
	}
	return ListRetrievers200JSONResponse(result), nil
}

func (h *Handlers) CreateRetriever(ctx context.Context, request CreateRetrieverRequestObject) (CreateRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CreateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	refreshType := "incremental"
	if request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}

	cronExpr := request.Body.CronSchedule
	schedule, err := cron.Parse(cronExpr)
	if err != nil {
		return CreateRetriever422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "invalid cron expression: " + err.Error(),
			},
		}, nil
	}

	nextRun := schedule.Next(time.Now())

	retriever, err := h.queries.CreateRetriever(ctx, db.CreateRetrieverParams{
		WorkspaceID:    workspaceIDFromRequest(request.Id),
		CreatedBy:      uid,
		Name:           request.Body.Name,
		DefinitionSql:  request.Body.DefinitionSql,
		DestCatalog:    extractDestCatalog(request.Body.DefinitionSql),
		DestNamespace:  "",
		DestTable:      "",
		CronSchedule:   cronExpr,
		RefreshType:    refreshType,
		NextRefreshAt:  pgtype.Timestamptz{Time: nextRun, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	return CreateRetriever201JSONResponse(dbRetrieverToPublic(retriever)), nil
}

func (h *Handlers) GetRetriever(ctx context.Context, request GetRetrieverRequestObject) (GetRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	retriever, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		return GetRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "retriever not found"}}, nil
	}

	return GetRetriever200JSONResponse(dbRetrieverToPublic(retriever)), nil
}

func (h *Handlers) UpdateRetriever(ctx context.Context, request UpdateRetrieverRequestObject) (UpdateRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return UpdateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	current, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		return UpdateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "retriever not found"}}, nil
	}

	name := current.Name
	sql := current.DefinitionSql
	cronExpr := current.CronSchedule
	refreshType := current.RefreshType
	status := current.Status

	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.CronSchedule != nil {
		cronExpr = *request.Body.CronSchedule
	}
	if request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}
	if request.Body.Status != nil {
		status = string(*request.Body.Status)
	}

	schedule, err := cron.Parse(cronExpr)
	if err != nil {
		return UpdateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "invalid cron: " + err.Error()}}, nil
	}

	nextRun := schedule.Next(time.Now())

	updated, err := h.queries.UpdateRetriever(ctx, db.UpdateRetrieverParams{
		WorkspaceID:    workspaceIDFromRequest(request.Id),
		ID:             retrieverID,
		Name:           name,
		DefinitionSql:  sql,
		CronSchedule:   cronExpr,
		RefreshType:    refreshType,
		Status:         status,
		NextRefreshAt:  pgtype.Timestamptz{Time: nextRun, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	return UpdateRetriever200JSONResponse(dbRetrieverToPublic(updated)), nil
}

func (h *Handlers) DeleteRetriever(ctx context.Context, request DeleteRetrieverRequestObject) (DeleteRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return DeleteRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	if _, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	}); err != nil {
		return DeleteRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "retriever not found"}}, nil
	}

	return DeleteRetriever204Response{}, nil
}

func (h *Handlers) TriggerRetrieverRefresh(ctx context.Context, request TriggerRetrieverRefreshRequestObject) (TriggerRetrieverRefreshResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return TriggerRetrieverRefresh401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	retriever, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		return TriggerRetrieverRefresh404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "retriever not found"}}, nil
	}

	refreshType := retriever.RefreshType
	if request.Body != nil && request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}

	executionID := fmt.Sprintf("%x-%s", retrieverID.Bytes, time.Now().Format("20060102150405"))

	wo := client.StartWorkflowOptions{
		ID:        "retriever-refresh-" + executionID,
		TaskQueue: "baleia-task-queue",
	}

	_, err = h.temporalClient.ExecuteWorkflow(ctx, wo, workflows.RetrieverRefreshWorkflow,
		openapi_types.UUID(retriever.ID.Bytes).String(),
		refreshType,
	)
	if err != nil {
		return nil, err
	}

	return TriggerRetrieverRefresh202JSONResponse{
		Id:     openapi_types.UUID(retrieverID.Bytes),
		Status: RetrieverRefreshHistoryStatus("running"),
	}, nil
}

func (h *Handlers) GetRetrieverHistory(ctx context.Context, request GetRetrieverHistoryRequestObject) (GetRetrieverHistoryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetRetrieverHistory401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	limit := int32(50)
	history, err := h.queries.ListRetrieverRefreshHistory(ctx, db.ListRetrieverRefreshHistoryParams{
		RetrieverID: retrieverID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	result := make([]RetrieverRefreshHistory, 0, len(history))
	for _, h := range history {
		result = append(result, dbRetrieverHistoryToPublic(h))
	}
	return GetRetrieverHistory200JSONResponse{Data: &result}, nil
}

func dbRetrieverToPublic(r db.Retriever) Retriever {
	id := openapi_types.UUID(r.ID.Bytes)
	workspaceID := openapi_types.UUID(r.WorkspaceID.Bytes)
	createdBy := openapi_types.UUID(r.CreatedBy.Bytes)

	ret := Retriever{
		Id:             id,
		WorkspaceId:    workspaceID,
		CreatedBy:      &createdBy,
		Name:           r.Name,
		DefinitionSql:  r.DefinitionSql,
		DestCatalog:    r.DestCatalog,
		DestNamespace:  r.DestNamespace,
		DestTable:      r.DestTable,
		CronSchedule:   r.CronSchedule,
		RefreshType:    RetrieverRefreshType(r.RefreshType),
		Status:         RetrieverStatus(r.Status),
		CreatedAt:      pgTimestampToTime(r.CreatedAt),
	}
	if r.LastRefreshAt.Valid {
		ret.LastRefreshAt = &r.LastRefreshAt.Time
	}
	if r.NextRefreshAt.Valid {
		ret.NextRefreshAt = &r.NextRefreshAt.Time
	}
	return ret
}

func dbRetrieverHistoryToPublic(h db.RetrieverRefreshHistory) RetrieverRefreshHistory {
	id := openapi_types.UUID(h.ID.Bytes)
	retrieverID := openapi_types.UUID(h.RetrieverID.Bytes)

	var finishedAt *time.Time
	if h.FinishedAt.Valid {
		finishedAt = &h.FinishedAt.Time
	}

	var rowsProcessed *int
	if h.RowsProcessed != nil {
		v := int(*h.RowsProcessed)
		rowsProcessed = &v
	}

	var durationMs *int
	if h.DurationMs != nil {
		v := int(*h.DurationMs)
		durationMs = &v
	}

	return RetrieverRefreshHistory{
		Id:                   id,
		RetrieverId:         retrieverID,
		TemporalWorkflowId:  &h.TemporalWorkflowID,
		RefreshType:         RetrieverRefreshHistoryRefreshType(h.RefreshType),
		Status:              RetrieverRefreshHistoryStatus(h.Status),
		RowsProcessed:       rowsProcessed,
		DurationMs:          durationMs,
		ErrorMessage:        h.ErrorMessage,
		StartedAt:           pgTimestampToTime(h.StartedAt),
		FinishedAt:          finishedAt,
	}
}

func extractDestCatalog(sql string) string {
	return ""
}
