package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/retrievers"
	"github.com/galileostudio/baleia/internal/retrievers/schedule"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *QueryHandlers) ListRetrievers(ctx context.Context, request ListRetrieversRequestObject) (ListRetrieversResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return ListRetrievers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	all, err := h.retrievers.List(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}

	result := make([]Retriever, 0, len(all))
	for _, r := range all {
		result = append(result, h.retrieverToPublic(ctx, r))
	}
	return ListRetrievers200JSONResponse(result), nil
}

func (h *QueryHandlers) CreateRetriever(ctx context.Context, request CreateRetrieverRequestObject) (CreateRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverCreate, ResourceType: "retriever", WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return CreateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	uid, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}

	// The destination is a binding the workspace may write to (#118), not a
	// bare catalog name.
	draft := retrievers.Draft{
		Name:          request.Body.Name,
		DefinitionSQL: request.Body.DefinitionSql,
		DestNamespace: request.Body.DestNamespace,
		DestTable:     request.Body.DestTable,
		CronSchedule:  request.Body.CronSchedule,
	}
	if request.Body.DestBindingId != nil {
		draft.DestBindingID = &pgtype.UUID{Bytes: *request.Body.DestBindingId, Valid: true}
	}
	if request.Body.DestCatalog != nil {
		draft.DestCatalog = *request.Body.DestCatalog
	}
	if request.Body.RefreshType != nil {
		draft.RefreshType = string(*request.Body.RefreshType)
	}
	retriever, err := h.retrievers.Create(ctx, workspaceIDFromRequest(request.Id), uid, draft)
	if code, msg, ok := retrieverRejection(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateRetriever422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: code, Message: msg}}, nil
	}
	if err != nil {
		return nil, err
	}

	h.audited(ctx, withID(ev, retriever.ID).Allowed())
	return CreateRetriever201JSONResponse(h.retrieverToPublic(ctx, retriever)), nil
}

func (h *QueryHandlers) GetRetriever(ctx context.Context, request GetRetrieverRequestObject) (GetRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return GetRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	retriever, err := h.retrievers.Get(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.RetrieverId, Valid: true})
	if errors.Is(err, retrievers.ErrNotFound) {
		return GetRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	return GetRetriever200JSONResponse(h.retrieverToPublic(ctx, retriever)), nil
}

func (h *QueryHandlers) UpdateRetriever(ctx context.Context, request UpdateRetrieverRequestObject) (UpdateRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverUpdate, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return UpdateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	b := request.Body
	change := retrievers.Change{
		Name:          b.Name,
		DefinitionSQL: b.DefinitionSql,
		DestNamespace: b.DestNamespace,
		DestTable:     b.DestTable,
		CronSchedule:  b.CronSchedule,
	}
	if b.DestBindingId != nil {
		change.DestBindingID = &pgtype.UUID{Bytes: *b.DestBindingId, Valid: true}
	}
	if b.RefreshType != nil {
		v := string(*b.RefreshType)
		change.RefreshType = &v
	}
	if b.Status != nil {
		v := string(*b.Status)
		change.Status = &v
	}
	updated, err := h.retrievers.Update(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.RetrieverId, Valid: true}, change)
	if errors.Is(err, retrievers.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}
	if code, msg, ok := retrieverRejection(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{Code: code, Message: msg}, nil
	}
	if err != nil {
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return UpdateRetriever200JSONResponse(h.retrieverToPublic(ctx, updated)), nil
}

func (h *QueryHandlers) DeleteRetriever(ctx context.Context, request DeleteRetrieverRequestObject) (DeleteRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverDelete, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return DeleteRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	err := h.retrievers.Delete(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.RetrieverId, Valid: true})
	if errors.Is(err, retrievers.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return DeleteRetriever204Response{}, nil
}

func (h *QueryHandlers) TriggerRetrieverRefresh(ctx context.Context, request TriggerRetrieverRefreshRequestObject) (TriggerRetrieverRefreshResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverRefresh, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return TriggerRetrieverRefresh401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	refreshType := ""
	if request.Body != nil && request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}
	err := h.retrievers.Refresh(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.RetrieverId, Valid: true}, refreshType)
	switch {
	case errors.Is(err, retrievers.ErrNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return TriggerRetrieverRefresh404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	case errors.Is(err, retrievers.ErrRefreshNotSupported):
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return TriggerRetrieverRefresh422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeNotSupported, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return TriggerRetrieverRefresh202JSONResponse{
		Id:     request.RetrieverId,
		Status: RetrieverRefreshHistoryStatus("running"),
	}, nil
}

func (h *QueryHandlers) GetRetrieverHistory(ctx context.Context, request GetRetrieverHistoryRequestObject) (GetRetrieverHistoryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return GetRetrieverHistory401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	history, err := h.retrievers.History(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.RetrieverId, Valid: true})
	if errors.Is(err, retrievers.ErrNotFound) {
		return GetRetrieverHistory404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	result := make([]RetrieverRefreshHistory, 0, len(history))
	for _, run := range history {
		result = append(result, dbRetrieverHistoryToPublic(run))
	}
	return GetRetrieverHistory200JSONResponse{Data: &result}, nil
}

func (h *QueryHandlers) ListRetrieverDestinations(ctx context.Context, request ListRetrieverDestinationsRequestObject) (ListRetrieverDestinationsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return ListRetrieverDestinations401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	rows, err := h.retrievers.Destinations(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}
	out := make([]RetrieverDestination, 0, len(rows))
	for _, row := range rows {
		out = append(out, retrieverDestinationToPublic(row))
	}
	return ListRetrieverDestinations200JSONResponse(out), nil
}

// ValidateRetriever parses the definition and explains it on the destination
// engine in a read-only session with the caller's identity (#118).
func (h *QueryHandlers) ValidateRetriever(ctx context.Context, request ValidateRetrieverRequestObject) (ValidateRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return ValidateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}
	if request.Body == nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	uid, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	identity, err := h.trinoIdentityFor(ctx, uid)
	if err != nil {
		return nil, err
	}

	err = h.retrievers.Validate(ctx, workspaceIDFromRequest(request.Id), pgtype.UUID{Bytes: request.Body.DestBindingId, Valid: true}, request.Body.DefinitionSql, identity)
	switch {
	case errors.Is(err, retrievers.ErrBindingNotFound):
		return ValidateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	case errors.Is(err, retrievers.ErrEngineUnavailable):
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeEngineUnavailable, err.Error())}, nil
	case errors.Is(err, retrievers.ErrExplainNotSupported):
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, err.Error())}, nil
	}
	if code, msg, ok := retrieverRejection(err); ok {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(code, msg)}, nil
	}
	if err != nil {
		return nil, err
	}
	return ValidateRetriever200JSONResponse{Ok: true}, nil
}

// PreviewCron returns the next five runs from the server's parser; the browser
// never evaluates cron itself (#118).
func (h *QueryHandlers) PreviewCron(ctx context.Context, request PreviewCronRequestObject) (PreviewCronResponseObject, error) {
	if auth.UserIDFromContext(ctx) == "" {
		return PreviewCron401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil || strings.TrimSpace(request.Body.Expression) == "" {
		return PreviewCron422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "expression is required")}, nil
	}

	p := schedule.Next5(request.Body.Expression)
	if !p.Valid {
		msg := p.Error
		return PreviewCron200JSONResponse{Valid: false, Error: &msg, Next: []time.Time{}, MinIntervalOk: false}, nil
	}
	return PreviewCron200JSONResponse{Valid: true, Next: p.Next, MinIntervalOk: p.MinIntervalOK}, nil
}

// retrieverRejection maps a rejected retriever write to its 422 code and
// message; ok is false for any other error.
func retrieverRejection(err error) (code ErrorCode, msg string, ok bool) {
	var invalid *retrievers.InvalidError
	switch {
	case errors.As(err, &invalid):
		return ErrorCodeValidationError, invalid.Msg, true
	case errors.Is(err, retrievers.ErrRefreshNotSupported):
		return ErrorCodeNotSupported, err.Error(), true
	case errors.Is(err, retrievers.ErrDestinationNotEligible):
		return ErrorCodeDestinationNotEligible, err.Error(), true
	}
	return "", "", false
}

func (h *QueryHandlers) retrieverToPublic(ctx context.Context, r db.Retriever) Retriever {
	id := openapi_types.UUID(r.ID.Bytes)
	workspaceID := openapi_types.UUID(r.WorkspaceID.Bytes)
	createdBy := openapi_types.UUID(r.CreatedBy.Bytes)

	ret := Retriever{
		Id:            id,
		WorkspaceId:   workspaceID,
		CreatedBy:     &createdBy,
		Name:          r.Name,
		DefinitionSql: r.DefinitionSql,
		DestCatalog:   r.DestCatalog,
		DestNamespace: r.DestNamespace,
		DestTable:     r.DestTable,
		CronSchedule:  r.CronSchedule,
		RefreshType:   RetrieverRefreshType(r.RefreshType),
		Status:        RetrieverStatus(r.Status),
		CreatedAt:     pgTimestampToTime(r.CreatedAt),
		ErrorCode:     r.ErrorCode,
	}
	if r.DestBindingID.Valid {
		bindingID := openapi_types.UUID(r.DestBindingID.Bytes)
		ret.DestBindingId = &bindingID
		// Nil when the binding, its catalog or its engine was deleted.
		if d, ok := h.retrievers.DestinationOf(ctx, r.DestBindingID); ok {
			connector := ""
			if d.Binding.Connector != nil {
				connector = *d.Binding.Connector
			}
			dest := retrieverDestinationToPublic(db.ListRetrieverDestinationsRow{
				BindingID:         d.Binding.ID,
				EngineCatalogName: d.Binding.EngineCatalogName,
				Connector:         connector,
				CatalogID:         d.Catalog.ID,
				CatalogName:       d.Catalog.Name,
				EngineID:          d.Engine.ID,
				EngineName:        d.Engine.Name,
				EngineKind:        d.Engine.Kind,
			})
			ret.Destination = &dest
		}
	}
	actions := []string{"view", "update", "delete", "refresh", "pause"}
	ret.AllowedActions = &actions
	if status, ok := h.retrievers.LastRefreshStatus(ctx, r.ID); ok {
		ret.LastRefreshStatus = &status
	}
	if r.LastRefreshAt.Valid {
		ret.LastRefreshAt = &r.LastRefreshAt.Time
	}
	if r.NextRefreshAt.Valid {
		ret.NextRefreshAt = &r.NextRefreshAt.Time
	}
	if r.UpdatedAt.Valid {
		ret.UpdatedAt = &r.UpdatedAt.Time
	}
	return ret
}

func retrieverDestinationToPublic(row db.ListRetrieverDestinationsRow) RetrieverDestination {
	dest := RetrieverDestination{
		BindingId:         openapi_types.UUID(row.BindingID.Bytes),
		EngineCatalogName: row.EngineCatalogName,
		Connector:         row.Connector,
	}
	dest.Catalog.Id = openapi_types.UUID(row.CatalogID.Bytes)
	dest.Catalog.Name = row.CatalogName
	dest.Engine.Id = openapi_types.UUID(row.EngineID.Bytes)
	dest.Engine.Name = row.EngineName
	dest.Engine.Kind = row.EngineKind
	return dest
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
		Id:                 id,
		RetrieverId:        retrieverID,
		TemporalWorkflowId: &h.TemporalWorkflowID,
		RefreshType:        RetrieverRefreshHistoryRefreshType(h.RefreshType),
		Status:             RetrieverRefreshHistoryStatus(h.Status),
		RowsProcessed:      rowsProcessed,
		DurationMs:         durationMs,
		ErrorMessage:       h.ErrorMessage,
		ErrorCode:          h.ErrorCode,
		StartedAt:          pgTimestampToTime(h.StartedAt),
		FinishedAt:         finishedAt,
	}
}
