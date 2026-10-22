package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/client"
)

var destIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (h *QueryHandlers) ListRetrievers(ctx context.Context, request ListRetrieversRequestObject) (ListRetrieversResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListRetrievers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	retrievers, err := h.queries.ListRetrieversByWorkspace(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}

	result := make([]Retriever, 0, len(retrievers))
	for _, r := range retrievers {
		result = append(result, h.retrieverToPublic(ctx, r))
	}
	return ListRetrievers200JSONResponse(result), nil
}

func (h *QueryHandlers) CreateRetriever(ctx context.Context, request CreateRetrieverRequestObject) (CreateRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverCreate, ResourceType: "retriever", WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return CreateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	wsID := workspaceIDFromRequest(request.Id)
	for _, field := range []struct {
		value, name string
	}{
		{request.Body.DestNamespace, "dest_namespace"},
		{request.Body.DestTable, "dest_table"},
	} {
		if err := validateDestIdentifier(field.value, field.name); err != nil {
			h.audited(ctx, ev.Failed(audit.ReasonValidation))
			return CreateRetriever422JSONResponse{
				UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
					Code:    ErrorCodeValidationError,
					Message: err.Error(),
				},
			}, nil
		}
	}

	if err := h.validateRetrieverDefinition(request.Body.DefinitionSql); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateRetriever422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeValidationError,
				Message: err.Error(),
			},
		}, nil
	}

	refreshType := "full"
	if request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}
	if refreshType != "full" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateRetriever422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeNotSupported,
				Message: "only 'full' refresh is supported in v1; 'incremental' requires a separate design",
			},
		}, nil
	}

	// The destination is a binding the workspace may write to (#118), not a
	// bare catalog name.
	catalogName := ""
	if request.Body.DestCatalog != nil {
		catalogName = *request.Body.DestCatalog
	}
	dest, err := h.resolveRetrieverDestination(ctx, wsID, request.Body.DestBindingId, catalogName)
	if err != nil {
		return nil, err
	}
	if dest == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateRetriever422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeDestinationNotEligible,
				Message: "the destination must be an active Iceberg binding on a federated engine with a write grant",
			},
		}, nil
	}

	cronExpr := request.Body.CronSchedule
	schedule, loc, err := parseRetrieverCron(cronExpr)
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateRetriever422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeValidationError,
				Message: err.Error(),
			},
		}, nil
	}

	nextRun := schedule.Next(time.Now().In(loc))

	retriever, err := h.queries.CreateRetriever(ctx, db.CreateRetrieverParams{
		WorkspaceID:   wsID,
		CreatedBy:     uid,
		Name:          request.Body.Name,
		DefinitionSql: request.Body.DefinitionSql,
		DestCatalog:   dest.EngineCatalogName,
		DestNamespace: request.Body.DestNamespace,
		DestTable:     request.Body.DestTable,
		DestBindingID: pgtype.UUID{Bytes: [16]byte(dest.BindingId), Valid: true},
		CronSchedule:  cronExpr,
		RefreshType:   refreshType,
		NextRefreshAt: pgtype.Timestamptz{Time: nextRun, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	h.audited(ctx, withID(ev, retriever.ID).Allowed())
	return CreateRetriever201JSONResponse(h.retrieverToPublic(ctx, retriever)), nil
}

func (h *QueryHandlers) GetRetriever(ctx context.Context, request GetRetrieverRequestObject) (GetRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	retriever, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		return GetRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}

	return GetRetriever200JSONResponse(h.retrieverToPublic(ctx, retriever)), nil
}

func (h *QueryHandlers) UpdateRetriever(ctx context.Context, request UpdateRetrieverRequestObject) (UpdateRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverUpdate, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return UpdateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	current, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}

	name := current.Name
	sql := current.DefinitionSql
	destNamespace := current.DestNamespace
	destTable := current.DestTable
	bindingID := current.DestBindingID
	cronExpr := current.CronSchedule
	refreshType := current.RefreshType
	status := current.Status

	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.DefinitionSql != nil {
		sql = *request.Body.DefinitionSql
	}
	if request.Body.DestNamespace != nil {
		destNamespace = *request.Body.DestNamespace
	}
	if request.Body.DestTable != nil {
		destTable = *request.Body.DestTable
	}
	if request.Body.DestBindingId != nil {
		bindingID = pgtype.UUID{Bytes: [16]byte(*request.Body.DestBindingId), Valid: true}
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
	if refreshType != "full" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{
			Code:    ErrorCodeNotSupported,
			Message: "only 'full' refresh is supported in v1; 'incremental' requires a separate design",
		}, nil
	}
	if err := h.validateRetrieverDefinition(sql); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}, nil
	}
	if err := validateDestIdentifier(destNamespace, "dest_namespace"); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}, nil
	}
	if err := validateDestIdentifier(destTable, "dest_table"); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}, nil
	}

	dest, err := h.retrieverDestinationByID(ctx, workspaceIDFromRequest(request.Id), bindingID, current.DestCatalog)
	if err != nil {
		return nil, err
	}
	if dest == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{
			Code:    ErrorCodeDestinationNotEligible,
			Message: "the destination must be an active Iceberg binding on a federated engine with a write grant",
		}, nil
	}

	schedule, loc, err := parseRetrieverCron(cronExpr)
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateRetriever422JSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}, nil
	}

	// An edit with a valid destination clears a previous destination error.
	var errorCode *string
	if status == "error" {
		status = "active"
	}
	nextRun := schedule.Next(time.Now().In(loc))

	updated, err := h.queries.UpdateRetriever(ctx, db.UpdateRetrieverParams{
		WorkspaceID:   workspaceIDFromRequest(request.Id),
		ID:            retrieverID,
		Name:          name,
		DefinitionSql: sql,
		DestCatalog:   dest.EngineCatalogName,
		DestNamespace: destNamespace,
		DestTable:     destTable,
		DestBindingID: pgtype.UUID{Bytes: [16]byte(dest.BindingId), Valid: true},
		CronSchedule:  cronExpr,
		RefreshType:   refreshType,
		Status:        status,
		ErrorCode:     errorCode,
		NextRefreshAt: pgtype.Timestamptz{Time: nextRun, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return UpdateRetriever200JSONResponse(h.retrieverToPublic(ctx, updated)), nil
}

func (h *QueryHandlers) DeleteRetriever(ctx context.Context, request DeleteRetrieverRequestObject) (DeleteRetrieverResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverDelete, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return DeleteRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	n, err := h.queries.DeleteRetriever(ctx, db.DeleteRetrieverParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}

	h.audited(ctx, ev.Allowed())
	return DeleteRetriever204Response{}, nil
}

func (h *QueryHandlers) TriggerRetrieverRefresh(ctx context.Context, request TriggerRetrieverRefreshRequestObject) (TriggerRetrieverRefreshResponseObject, error) {
	ev := audit.Event{Action: audit.RetrieverRefresh, ResourceType: "retriever", ResourceID: uuid.UUID(request.RetrieverId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return TriggerRetrieverRefresh401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	var retrieverID pgtype.UUID
	retrieverID.Bytes = request.RetrieverId
	retrieverID.Valid = true

	retriever, err := h.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		ID:          retrieverID,
	})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return TriggerRetrieverRefresh404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "retriever not found"}}, nil
	}

	refreshType := retriever.RefreshType
	if request.Body != nil && request.Body.RefreshType != nil {
		refreshType = string(*request.Body.RefreshType)
	}
	if refreshType != "full" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return TriggerRetrieverRefresh422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeNotSupported,
				Message: "only 'full' refresh is supported in v1; 'incremental' requires a separate design",
			},
		}, nil
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

	h.audited(ctx, ev.Allowed())
	return TriggerRetrieverRefresh202JSONResponse{
		Id:     openapi_types.UUID(retrieverID.Bytes),
		Status: RetrieverRefreshHistoryStatus("running"),
	}, nil
}

func (h *QueryHandlers) GetRetrieverHistory(ctx context.Context, request GetRetrieverHistoryRequestObject) (GetRetrieverHistoryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetRetrieverHistory401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
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

func (h *QueryHandlers) ListRetrieverDestinations(ctx context.Context, request ListRetrieverDestinationsRequestObject) (ListRetrieverDestinationsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListRetrieverDestinations401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}

	rows, err := h.queries.ListRetrieverDestinations(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}
	out := make([]RetrieverDestination, 0, len(rows))
	for _, row := range rows {
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
		out = append(out, dest)
	}
	return ListRetrieverDestinations200JSONResponse(out), nil
}

// ValidateRetriever parses the definition and explains it on the destination
// engine in a read-only session with the caller's identity (#118).
func (h *QueryHandlers) ValidateRetriever(ctx context.Context, request ValidateRetrieverRequestObject) (ValidateRetrieverResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ValidateRetriever401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}
	if request.Body == nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	if err := h.validateRetrieverDefinition(request.Body.DefinitionSql); err != nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}

	dest, err := h.resolveRetrieverDestination(ctx, workspaceIDFromRequest(request.Id), &request.Body.DestBindingId, "")
	if err != nil {
		return nil, err
	}
	if dest == nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeDestinationNotEligible, "the destination must be an active Iceberg binding on a federated engine with a write grant")}, nil
	}
	if h.engines == nil {
		return nil, errors.New("retriever validation: no engine registry")
	}

	binding, err := h.queries.GetBinding(ctx, pgtype.UUID{Bytes: [16]byte(dest.BindingId), Valid: true})
	if err != nil {
		return ValidateRetriever404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	}
	eng, err := h.engines.Get(ctx, binding.EngineID)
	if err != nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeEngineUnavailable, "the destination engine is not available")}, nil
	}
	d := eng.Dialect()
	explained, supported := d.ExplainSQL(request.Body.DefinitionSql)
	if !supported {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, "this engine cannot explain the definition")}, nil
	}

	uid, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	identity, err := h.trinoIdentityFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = queryengine.CollectStrings(vctx, eng, queryengine.SessionSpec{
		ReadOnly: true,
		Catalog:  binding.EngineCatalogName,
		Identity: queryengine.Identity{UserID: identity},
		Tags:     queryengine.Tags{Source: "retriever-validate"},
	}, explained, 1)
	if err != nil {
		return ValidateRetriever422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
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

	loc, clean := cronLocation(request.Body.Expression)
	schedule, err := cron.ParseStandard(clean)
	if err != nil {
		msg := "invalid cron expression: " + err.Error()
		return PreviewCron200JSONResponse{Valid: false, Error: &msg, Next: []time.Time{}, MinIntervalOk: false}, nil
	}

	next := make([]time.Time, 0, 5)
	ref := time.Now().In(loc)
	for i := 0; i < 5; i++ {
		ref = schedule.Next(ref)
		next = append(next, ref.UTC())
	}
	minIntervalOK := true
	for i := 1; i < len(next); i++ {
		if next[i].Sub(next[i-1]) < 5*time.Minute {
			minIntervalOK = false
			break
		}
	}
	return PreviewCron200JSONResponse{Valid: true, Next: next, MinIntervalOk: minIntervalOK}, nil
}

func (h *core) retrieverToPublic(ctx context.Context, r db.Retriever) Retriever {
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
		ret.Destination = h.retrieverDestination(ctx, r.DestBindingID)
	}
	actions := []string{"view", "update", "delete", "refresh", "pause"}
	ret.AllowedActions = &actions
	if latest, err := h.queries.LatestRetrieverRefresh(ctx, r.ID); err == nil {
		status := latest.Status
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

// retrieverDestination loads the binding, catalog and engine a retriever
// materializes into. Nil when the binding was deleted.
func (h *core) retrieverDestination(ctx context.Context, bindingID pgtype.UUID) *RetrieverDestination {
	binding, err := h.queries.GetBinding(ctx, bindingID)
	if err != nil {
		return nil
	}
	catalog, err := h.queries.GetCatalogByID(ctx, binding.CatalogID)
	if err != nil {
		return nil
	}
	engine, err := h.queries.GetEngineByID(ctx, binding.EngineID)
	if err != nil {
		return nil
	}
	connector := ""
	if binding.Connector != nil {
		connector = *binding.Connector
	}
	out := &RetrieverDestination{
		BindingId:         openapi_types.UUID(binding.ID.Bytes),
		EngineCatalogName: binding.EngineCatalogName,
		Connector:         connector,
	}
	out.Catalog.Id = openapi_types.UUID(catalog.ID.Bytes)
	out.Catalog.Name = catalog.Name
	out.Engine.Id = openapi_types.UUID(engine.ID.Bytes)
	out.Engine.Name = engine.Name
	out.Engine.Kind = engine.Kind
	return out
}

// resolveRetrieverDestination picks the destination from a binding id or, for
// legacy clients, from the catalogs name; it returns nil when the workspace
// cannot write to it.
func (h *core) resolveRetrieverDestination(ctx context.Context, workspaceID pgtype.UUID, bindingID *openapi_types.UUID, catalogName string) (*RetrieverDestination, error) {
	rows, err := h.queries.ListRetrieverDestinations(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		matches := false
		if bindingID != nil {
			matches = row.BindingID.Bytes == *bindingID
		} else if catalogName != "" {
			matches = row.EngineCatalogName == catalogName || row.CatalogName == catalogName
		}
		if !matches {
			continue
		}
		out := &RetrieverDestination{
			BindingId:         openapi_types.UUID(row.BindingID.Bytes),
			EngineCatalogName: row.EngineCatalogName,
			Connector:         row.Connector,
		}
		out.Catalog.Id = openapi_types.UUID(row.CatalogID.Bytes)
		out.Catalog.Name = row.CatalogName
		out.Engine.Id = openapi_types.UUID(row.EngineID.Bytes)
		out.Engine.Name = row.EngineName
		out.Engine.Kind = row.EngineKind
		return out, nil
	}
	return nil, nil
}

// retrieverDestinationByID is the update path: current binding first, then the
// legacy catalog name.
func (h *core) retrieverDestinationByID(ctx context.Context, workspaceID pgtype.UUID, bindingID pgtype.UUID, catalogName string) (*RetrieverDestination, error) {
	if bindingID.Valid {
		id := openapi_types.UUID(bindingID.Bytes)
		return h.resolveRetrieverDestination(ctx, workspaceID, &id, "")
	}
	return h.resolveRetrieverDestination(ctx, workspaceID, nil, catalogName)
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

// validateDestIdentifier checks a destination identifier (catalog, namespace or
// table) against Trino's relaxed identifier rules before it is quoted into SQL.
func validateDestIdentifier(value, field string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	// Trino quoted identifiers allow more, but v1 restricts the shape the
	// retriever UI needs.
	if !destIdentifierRe.MatchString(value) {
		return fmt.Errorf("%s must match ^[a-zA-Z_][a-zA-Z0-9_]*$", field)
	}
	return nil
}

// validateRetrieverDefinition requires exactly one SELECT/WITH statement and
// rejects catalog DDL.
func (h *core) validateRetrieverDefinition(def string) error {
	// Retrievers refresh on the default Trino engine.
	stmts, err := h.dialects.MustGet(dialect.Trino).SplitStatements(def)
	if err != nil {
		return fmt.Errorf("definition_sql: %w", err)
	}
	if len(stmts) != 1 {
		return errors.New("definition_sql must be exactly one statement")
	}
	if policy.IsCatalogDDL(stmts[0]) {
		return errors.New("catalog management is not allowed in a retriever definition")
	}
	lower := strings.ToLower(strings.TrimSpace(stmts[0]))
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "with ") &&
		lower != "select" && lower != "with" {
		return errors.New("definition_sql must start with SELECT or WITH")
	}
	return nil
}

// parseRetrieverCron parses a standard five-field cron expression
// (minute hour day-of-month month day-of-week), optionally prefixed with
// CRON_TZ=<IANA>, and enforces the 5-minute minimum interval.
func parseRetrieverCron(expr string) (cron.Schedule, *time.Location, error) {
	loc, clean := cronLocation(expr)
	schedule, err := cron.ParseStandard(clean)
	if err != nil {
		return nil, loc, errors.New("invalid cron expression: " + err.Error())
	}
	if err := validateCronInterval(schedule, loc); err != nil {
		return nil, loc, err
	}
	return schedule, loc, nil
}

// cronLocation reads the optional CRON_TZ=<IANA> prefix and returns the
// expression the parser understands.
func cronLocation(expr string) (*time.Location, string) {
	expr = strings.TrimSpace(expr)
	const prefix = "CRON_TZ="
	if strings.HasPrefix(strings.ToUpper(expr), prefix) {
		rest := strings.TrimSpace(expr[len(prefix):])
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) == 2 {
			if loc, err := time.LoadLocation(strings.TrimSpace(parts[0])); err == nil {
				return loc, strings.TrimSpace(parts[1])
			}
		}
	}
	return time.UTC, expr
}

// validateCronInterval rejects schedules whose runs are closer than 5 minutes,
// sampled across three consecutive runs.
func validateCronInterval(schedule cron.Schedule, loc *time.Location) error {
	ref := time.Now().In(loc)
	t1 := schedule.Next(ref)
	t2 := schedule.Next(t1)
	t3 := schedule.Next(t2)
	if t2.Sub(t1) < 5*time.Minute || t3.Sub(t2) < 5*time.Minute {
		return errors.New("cron minimum interval is 5 minutes")
	}
	return nil
}
