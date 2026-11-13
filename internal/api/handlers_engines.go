package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// enginePingTimeout bounds the reachability check of a new engine.
const enginePingTimeout = 15 * time.Second

// isFKViolation reports a delete blocked by a foreign key: 23503 for NO
// ACTION, 23001 for ON DELETE RESTRICT (engines use RESTRICT).
func isFKViolation(e *db.PGError) bool {
	return manage.IsFKViolation(e)
}

func engineToPublic(e db.Engine) Engine {
	out := Engine{
		Id:           openapi_types.UUID(e.ID.Bytes),
		Name:         e.Name,
		Kind:         e.Kind,
		Class:        EngineClass(e.Class),
		ConnectionId: openapi_types.UUID(e.ConnectionID.Bytes),
		IdentityMode: EngineIdentityMode(e.IdentityMode),
		Enabled:      e.Enabled,
		CreatedAt:    pgTimestampToTime(e.CreatedAt),
	}
	// Cost routing is opt-in per engine (#550).
	costRouting := e.CostRoutingEnabled
	out.CostRoutingEnabled = &costRouting
	out.CostThresholdBytes = e.CostThresholdBytes
	out.UpdatedAt = pgTimestampToTimePtr(e.UpdatedAt)
	return out
}

func bindingToPublic(logger *slog.Logger, b db.CatalogBinding) CatalogBinding {
	return CatalogBinding{
		Id:                openapi_types.UUID(b.ID.Bytes),
		CatalogId:         openapi_types.UUID(b.CatalogID.Bytes),
		EngineId:          openapi_types.UUID(b.EngineID.Bytes),
		EngineCatalogName: b.EngineCatalogName,
		Connector:         b.Connector,
		Mode:              CatalogBindingMode(b.Mode),
		Enabled:           b.Enabled,
		ExtraProps:        ptrExtraProps(logger, b.ExtraProps),
		LifecycleState:    ptrLifecycle(b.LifecycleState),
		LastErrorCode:     b.LastErrorCode,
		LastErrorMessage:  b.LastErrorMessage,
	}
}

func ptrExtraProps(logger *slog.Logger, raw []byte) *map[string]string {
	m := extraPropsOf(logger, raw)
	return &m
}

func ptrLifecycle(s string) *CatalogBindingLifecycleState {
	if s == "" {
		return nil
	}
	v := CatalogBindingLifecycleState(s)
	return &v
}

func engineNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}
}

// ListEngines returns every engine to an admin, else the engines on the
// caller's connections and those hosting a catalog granted to them.
func (h *CatalogHandlers) ListEngines(ctx context.Context, _ ListEnginesRequestObject) (ListEnginesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListEngines401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engines, err := h.engineAdmin.List(ctx, p.UserID, p.IsAdmin())
	if err != nil {
		return nil, err
	}
	return ListEngines200JSONResponse(h.describeEngines(ctx, engines)), nil
}

func (h *CatalogHandlers) GetEngine(ctx context.Context, request GetEngineRequestObject) (GetEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	eng, _, err := h.authz().Engine(ctx, p, db.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return GetEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	return GetEngine200JSONResponse(h.describeEngine(ctx, eng)), nil
}

func (h *CatalogHandlers) UpdateEngine(ctx context.Context, request UpdateEngineRequestObject) (UpdateEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := db.UUID{Bytes: request.Id, Valid: true}
	_, _, err = h.authz().Engine(ctx, p, id, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return UpdateEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return UpdateEngine403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the connection owner or an admin may change this engine"}}, nil
	case err != nil:
		return nil, err
	}
	body := request.Body
	if body == nil {
		return UpdateEngine422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}
	// Validate before the write: a refused request must change nothing (#591).
	if body.CostThresholdBytes != nil {
		if *body.CostThresholdBytes < 1 {
			return UpdateEngine422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "cost_threshold_bytes must be at least 1")}, nil
		}
		if body.ClearCostThreshold != nil && *body.ClearCostThreshold {
			return UpdateEngine422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "cost_threshold_bytes and clear_cost_threshold are mutually exclusive")}, nil
		}
	}
	// One UPDATE applies enabled and the settings together (#591).
	eng, err := h.engineAdmin.Update(ctx, db.UpdateEngineParams{
		ID:                 id,
		Enabled:            body.Enabled,
		CostRoutingEnabled: body.CostRoutingEnabled,
		CostThresholdBytes: body.CostThresholdBytes,
		ClearCostThreshold: body.ClearCostThreshold,
	})
	if err != nil {
		return nil, err
	}
	return UpdateEngine200JSONResponse(h.withEngineActions(ctx, eng)), nil
}

// DeleteEngine removes an engine with its native catalogs and bindings. It is
// refused while managed bindings or running executions exist.
func (h *CatalogHandlers) DeleteEngine(ctx context.Context, request DeleteEngineRequestObject) (DeleteEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := db.UUID{Bytes: request.Id, Valid: true}
	_, _, err = h.authz().Engine(ctx, p, id, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DeleteEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DeleteEngine403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the connection owner or an admin may delete this engine"}}, nil
	case err != nil:
		return nil, err
	}

	var inUse *manage.InUseError
	err = h.engineAdmin.Delete(ctx, id)
	if errors.As(err, &inUse) {
		return DeleteEngine409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeEngineInUse, Message: inUse.Msg}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	return DeleteEngine204Response{}, nil
}

func (h *CatalogHandlers) ListEngineBindings(ctx context.Context, request ListEngineBindingsRequestObject) (ListEngineBindingsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListEngineBindings401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := db.UUID{Bytes: request.Id, Valid: true}
	_, conn, err := h.authz().Engine(ctx, p, id, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return ListEngineBindings404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	bindings, err := h.engineAdmin.Bindings(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogBinding, 0, len(bindings))
	full := p.IsAdmin() || (conn.CreatedBy.Valid && conn.CreatedBy.Bytes == p.UserID.Bytes)
	for _, b := range bindings {
		// A grantee sees only the bindings of catalogs granted to them.
		if !full {
			if _, _, err := h.authz().Catalog(ctx, p, b.CatalogID, authz.ActRead); err != nil {
				if errors.Is(err, authz.ErrNotFound) {
					continue
				}
				return nil, err
			}
		}
		out = append(out, h.bindingResponse(ctx, b, ""))
	}
	return ListEngineBindings200JSONResponse(out), nil
}

func (h *CatalogHandlers) ListCatalogBindings(ctx context.Context, request ListCatalogBindingsRequestObject) (ListCatalogBindingsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogBindings401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := db.UUID{Bytes: request.Id, Valid: true}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListCatalogBindings404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		}
		return nil, err
	}
	bindings, err := h.engineAdmin.CatalogBindings(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, h.bindingResponse(ctx, b, ""))
	}
	return ListCatalogBindings200JSONResponse(out), nil
}
