package api

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/catalogs/admin"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListAllCatalogs — GET /catalogs (#109). Logical catalogs the caller may
// view, with their bindings, freshness and allowed actions.
func (h *CatalogHandlers) ListAllCatalogs(ctx context.Context, request ListAllCatalogsRequestObject) (ListAllCatalogsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAllCatalogs401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	rows, err := h.queries.ListVisibleCatalogs(ctx, db.ListVisibleCatalogsParams{IsAdmin: p.IsAdmin(), UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	q := ""
	if request.Params.Q != nil {
		q = strings.ToLower(*request.Params.Q)
	}
	origin := ""
	if request.Params.Origin != nil {
		origin = *request.Params.Origin
	}
	providerKind := ""
	if request.Params.ProviderKind != nil {
		providerKind = *request.Params.ProviderKind
	}
	attention := request.Params.Attention != nil && *request.Params.Attention
	action := ""
	if request.Params.Action != nil {
		action = string(*request.Params.Action)
	}
	limit, offset := 50, 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}

	filtered := make([]CatalogSummary, 0, len(rows))
	for _, cat := range rows {
		sum := h.catalogSummary(ctx, cat, p)
		if q != "" && !strings.Contains(strings.ToLower(derefString(sum.Name)), q) {
			continue
		}
		if origin != "" && sum.Origin != origin {
			continue
		}
		if request.Params.ConnectionId != nil && cat.ConnectionID.Bytes != [16]byte(*request.Params.ConnectionId) {
			continue
		}
		if request.Params.EngineId != nil && !catalogHasEngine(sum, *request.Params.EngineId) {
			continue
		}
		if providerKind != "" && (sum.ProviderKind == nil || *sum.ProviderKind != providerKind) {
			continue
		}
		if attention && !sum.NeedsAttention {
			continue
		}
		if action != "" && !catalogAllows(sum, action) {
			continue
		}
		filtered = append(filtered, sum)
	}
	start := min(offset, len(filtered))
	end := min(start+limit, len(filtered))
	return ListAllCatalogs200JSONResponse(filtered[start:end]), nil
}

// GetCatalogDetail — GET /catalogs/{catalog_id} (#109).
func (h *CatalogHandlers) GetCatalogDetail(ctx context.Context, request GetCatalogDetailRequestObject) (GetCatalogDetailResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogDetail401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	cat, _, err := h.authz().Catalog(ctx, p, pgtype.UUID{Bytes: request.CatalogId, Valid: true}, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return GetCatalogDetail404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	sum := h.catalogSummary(ctx, cat, p)
	size := h.catalogAdmin.Size(ctx, cat.ID)
	sum.NamespaceCount, sum.TableCount = size.Namespaces, size.Tables
	return GetCatalogDetail200JSONResponse(sum), nil
}

// UpdateCatalogDetail — PATCH /catalogs/{catalog_id} (#109). Renames the
// display name; bundles and metrics reference catalogs by name.
func (h *CatalogHandlers) UpdateCatalogDetail(ctx context.Context, request UpdateCatalogDetailRequestObject) (UpdateCatalogDetailResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogDetail401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	cat, _, err := h.authz().Catalog(ctx, p, id, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return UpdateCatalogDetail404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return UpdateCatalogDetail403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to modify this catalog"}}, nil
	case err != nil:
		return nil, err
	}
	if request.Body == nil {
		return UpdateCatalogDetail422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	updated, err := h.catalogAdmin.Update(ctx, cat, admin.Edit{Name: request.Body.Name, Description: request.Body.Description})
	if errors.Is(err, admin.ErrEmptyName) {
		return UpdateCatalogDetail422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, audit.Event{Action: audit.CatalogUpdate, ResourceType: "catalog", ResourceID: idString(id),
		Changes: map[string]audit.Change{"name": {From: cat.Name, To: updated.Name}}}.Allowed())
	out := h.catalogWithStatus(ctx, updated)
	return UpdateCatalogDetail200JSONResponse(out), nil
}

// DeleteCatalogDetail — DELETE /catalogs/{catalog_id} (#109). Source catalogs
// are disabled and their managed bindings deprovisioned; native catalogs
// belong to their engine.
func (h *CatalogHandlers) DeleteCatalogDetail(ctx context.Context, request DeleteCatalogDetailRequestObject) (DeleteCatalogDetailResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogDetail401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.CatalogDelete, ResourceType: "catalog", ResourceID: idString(id)}
	ctx = audit.WithAction(ctx, ev.Action)
	cat, _, err := h.authz().Catalog(ctx, p, id, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DeleteCatalogDetail404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DeleteCatalogDetail403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to delete this catalog"}}, nil
	case err != nil:
		return nil, err
	}
	managed, err := h.catalogAdmin.Retire(ctx, cat)
	switch {
	case errors.Is(err, admin.ErrNotManaged):
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteCatalogDetail422JSONResponse{UnprocessableEntityJSONResponse: notManaged()}, nil
	case errors.Is(err, admin.ErrBindingBusy):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteCatalogDetail409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeBindingBusy, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	for _, b := range managed {
		eng, err := h.queries.GetEngineByID(ctx, b.EngineID)
		if err != nil {
			continue
		}
		cluster, err := h.queries.GetTrinoClusterByEngine(ctx, b.EngineID)
		if err != nil {
			continue
		}
		l := h.lifecycleOn(cluster, eng)
		if _, err := l.Deprovision(ctx, b); err != nil && !errors.Is(err, bindings.ErrConcurrentChange) {
			return nil, err
		}
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalogDetail204Response{}, nil
}

// GetCatalogImpact — GET /catalogs/{catalog_id}/impact (#109).
func (h *CatalogHandlers) GetCatalogImpact(ctx context.Context, request GetCatalogImpactRequestObject) (GetCatalogImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.catalogAdmin.Impact(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetCatalogImpact200JSONResponse(CatalogImpact{
		Bindings:              int(row.Bindings),
		Grants:                int(row.Grants),
		SavedQueriesTargeting: int(row.SavedQueriesTargeting),
		Metrics:               int(row.Metrics),
		Docs:                  int(row.Docs),
		RunningExecutions:     int(row.RunningExecutions),
	}), nil
}

// GetCatalogConnectionImpact — GET /catalog-connections/{id}/impact (#109).
func (h *CatalogHandlers) GetCatalogConnectionImpact(ctx context.Context, request GetCatalogConnectionImpactRequestObject) (GetCatalogConnectionImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogConnectionImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.authz().Connection(ctx, p, id, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogConnectionImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	impact, err := h.catalogAdmin.ConnectionImpact(ctx, id)
	if err != nil {
		return nil, err
	}
	out := CatalogConnectionImpact{
		Catalogs:          int(impact.Catalogs),
		Bindings:          int(impact.Bindings),
		RunningExecutions: int(impact.RunningExecutions),
	}
	if e := impact.Engine; e != nil {
		out.Engine = &EngineConnectionRef{Id: openapi_types.UUID(e.ID.Bytes), Name: e.Name}
	}
	return GetCatalogConnectionImpact200JSONResponse(out), nil
}

// ListCatalogGrants — GET /catalogs/{catalog_id}/grants (#109).
func (h *CatalogHandlers) ListCatalogGrants(ctx context.Context, request ListCatalogGrantsRequestObject) (ListCatalogGrantsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogGrants401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return ListCatalogGrants404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := h.queries.ListCatalogGrants(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogGrant, 0, len(rows))
	for _, r := range rows {
		g := CatalogGrant{
			Permission: CatalogGrantPermission(r.Permission),
			GrantedAt:  pgTimestampToTime(r.GrantedAt),
		}
		g.Workspace.Id = openapi_types.UUID(r.WorkspaceID.Bytes)
		g.Workspace.Name = r.WorkspaceName
		kind := r.WorkspaceType
		g.Workspace.Type = &kind
		if r.GrantedBy.Valid {
			by := openapi_types.UUID(r.GrantedBy.Bytes)
			g.GrantedBy = &by
		}
		out = append(out, g)
	}
	return ListCatalogGrants200JSONResponse(out), nil
}

// GetCatalogBinding — GET /bindings/{id} (#109).
func (h *CatalogHandlers) GetCatalogBinding(ctx context.Context, request GetCatalogBindingRequestObject) (GetCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	b, err := h.queries.GetBinding(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetCatalogBinding404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, _, err := h.authz().Catalog(ctx, p, b.CatalogID, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogBinding404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	return GetCatalogBinding200JSONResponse(h.bindingResponse(ctx, b, "")), nil
}

// GetCatalogBindingProjection — GET /bindings/{id}/projection (#109). Secret
// values are replaced by ***; env references are shown as-is.
func (h *CatalogHandlers) GetCatalogBindingProjection(ctx context.Context, request GetCatalogBindingProjectionRequestObject) (GetCatalogBindingProjectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogBindingProjection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	b, err := h.queries.GetBinding(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetCatalogBindingProjection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	cat, _, err := h.authz().Catalog(ctx, p, b.CatalogID, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return GetCatalogBindingProjection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GetCatalogBindingProjection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to see this binding's projection"}}, nil
	case err != nil:
		return nil, err
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		return nil, err
	}
	spec, err := catalogSpec(cat)
	if err != nil {
		return GetCatalogBindingProjection422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	proj, err := h.projectBinding(ctx, conn, b.EngineID, b.CatalogID, spec)
	if err != nil {
		return GetCatalogBindingProjection422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	properties := make(map[string]string, len(proj.Properties))
	for k, v := range proj.Properties {
		properties[k] = v
	}
	for _, key := range proj.SecretKeys {
		if _, ok := properties[key]; ok {
			properties[key] = "***"
		}
	}
	out := BindingProjection{Connector: proj.Connector, Properties: properties}
	if proj.Version != "" {
		version := proj.Version
		out.Version = &version
	}
	return GetCatalogBindingProjection200JSONResponse(out), nil
}

// catalogSummary builds the list/detail item: the catalog with its status,
// visibility-scoped connection, bindings and attention flag.
func (h *core) catalogSummary(ctx context.Context, cat db.Catalog, p authz.Principal) CatalogSummary {
	c := h.catalogWithStatus(ctx, cat)
	out := CatalogSummary{
		AllowedActions:        c.AllowedActions,
		CatalogVersion:        c.CatalogVersion,
		ConnectionId:          c.ConnectionId,
		Connector:             c.Connector,
		CreatedAt:             c.CreatedAt,
		Description:           c.Description,
		Enabled:               c.Enabled,
		Id:                    c.Id,
		MetadataSyncErrorCode: c.MetadataSyncErrorCode,
		MetadataSyncedAt:      c.MetadataSyncedAt,
		Name:                  c.Name,
		Origin:                cat.Origin,
		SourceRef:             cat.SourceRef,
		SyncError:             c.SyncError,
		TrinoCatalogName:      c.TrinoCatalogName,
		UpdatedAt:             c.UpdatedAt,
		Warehouse:             c.Warehouse,
		Bindings:              []CatalogBindingRef{},
	}
	if c.SyncStatus != nil {
		status := CatalogSummarySyncStatus(*c.SyncStatus)
		out.SyncStatus = &status
	}
	if c.MetadataSyncStatus != nil {
		status := CatalogSummaryMetadataSyncStatus(*c.MetadataSyncStatus)
		out.MetadataSyncStatus = &status
	}
	if conn, err := h.authz().Connection(ctx, p, cat.ConnectionID, authz.ActRead); err == nil {
		out.Connection = &EngineConnectionRef{Id: openapi_types.UUID(conn.ID.Bytes), Name: conn.Name}
		kind := conn.Type
		out.ProviderKind = &kind
	}
	rows, err := h.queries.ListBindingsByCatalog(ctx, cat.ID)
	if err == nil {
		for _, b := range rows {
			ref := CatalogBindingRef{
				Id:                openapi_types.UUID(b.ID.Bytes),
				EngineCatalogName: b.EngineCatalogName,
				Connector:         b.Connector,
				Mode:              b.Mode,
				LifecycleState:    b.LifecycleState,
				StateChangedAt:    pgTimestampToTimePtr(b.StateChangedAt),
			}
			if eng, err := h.queries.GetEngineByID(ctx, b.EngineID); err == nil {
				ref.Engine = CatalogEngineRef{
					Id: openapi_types.UUID(eng.ID.Bytes), Name: eng.Name, Kind: eng.Kind, Class: eng.Class,
				}
			}
			status, _ := h.bindingSyncStatus(ctx, b)
			ref.SyncStatus = &status
			if b.LifecycleState == "error" || b.LifecycleState == "provisioning" || b.LifecycleState == "updating" || b.LifecycleState == "deprovisioning" {
				out.NeedsAttention = true
			}
			out.Bindings = append(out.Bindings, ref)
		}
	}
	if out.MetadataSyncStatus != nil && *out.MetadataSyncStatus == CatalogSummaryMetadataSyncStatusFailed {
		out.NeedsAttention = true
	}
	return out
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func catalogHasEngine(c CatalogSummary, engineID openapi_types.UUID) bool {
	for _, b := range c.Bindings {
		if b.Engine.Id == engineID {
			return true
		}
	}
	return false
}

func catalogAllows(c CatalogSummary, action string) bool {
	if c.AllowedActions == nil {
		return false
	}
	for _, allowed := range *c.AllowedActions {
		if string(allowed) == action {
			return true
		}
	}
	return false
}
