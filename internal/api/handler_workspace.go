package api

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	wsmanage "github.com/galileostudio/baleia/internal/workspaces/manage"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *WorkspaceHandlers) ListWorkspaces(ctx context.Context, request ListWorkspacesRequestObject) (ListWorkspacesResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return ListWorkspaces401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	workspaces, err := h.workspaces.ListForUser(ctx, uid)
	if err != nil {
		return nil, err
	}

	result := make([]Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		result = append(result, h.withWorkspaceActions(ctx, w))
	}
	return ListWorkspaces200JSONResponse(result), nil
}

func (h *WorkspaceHandlers) CreateWorkspace(ctx context.Context, request CreateWorkspaceRequestObject) (CreateWorkspaceResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	role := auth.RoleFromContext(ctx)

	var ownerUserID pgtype.UUID
	var ownerTeamID pgtype.UUID

	wsType := string(request.Body.Type)

	if wsType == "shared" {
		if role != "admin" {
			return CreateWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only for shared workspaces"}}, nil
		}
		if request.Body.OwnerTeamId == nil {
			return CreateWorkspace422JSONResponse{
				UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
					Code:    ErrorCodeValidationError,
					Message: "owner_team_id is required for shared workspaces",
					Errors: []struct {
						Field   *string `json:"field,omitempty"`
						Message *string `json:"message,omitempty"`
					}{{fieldPtr("owner_team_id"), msgPtr("required")}},
				},
			}, nil
		}
		ownerTeamID.Bytes = *request.Body.OwnerTeamId
		ownerTeamID.Valid = true
	} else {
		ownerUserID = uid
	}

	workspace, err := h.workspaces.Create(ctx, wsmanage.NewWorkspace{
		Name:        request.Body.Name,
		Description: request.Body.Description,
		Type:        wsType,
		OwnerUser:   ownerUserID,
		OwnerTeam:   ownerTeamID,
	})
	if err != nil {
		return nil, err
	}

	return CreateWorkspace201JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

func (h *WorkspaceHandlers) GetWorkspace(ctx context.Context, request GetWorkspaceRequestObject) (GetWorkspaceResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return GetWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	workspace, err := h.workspaces.Get(ctx, uid)
	if errors.Is(err, wsmanage.ErrNotFound) {
		return GetWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}

	return GetWorkspace200JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

func (h *WorkspaceHandlers) UpdateWorkspace(ctx context.Context, request UpdateWorkspaceRequestObject) (UpdateWorkspaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	uid := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.WorkspaceUpdate, ResourceType: "workspace", ResourceID: idString(uid), WorkspaceID: idString(uid)}
	ctx = audit.WithAction(ctx, ev.Action)
	current, err := h.authz().Workspace(ctx, p, uid, authz.ActWrite)
	if err != nil {
		switch {
		case errors.Is(err, authz.ErrNotFound):
			return UpdateWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		case errors.Is(err, authz.ErrForbidden):
			return UpdateWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace owner may change it"}}, nil
		}
		return nil, err
	}

	var newDefault, clearDefault bool
	var defaultBinding pgtype.UUID
	var defaultSchema *string
	if dt := request.Body.DefaultTarget; dt != nil {
		if dt.BindingId == nil {
			clearDefault = true
		} else {
			target, err := h.ResolveTarget(ctx, current, &TargetRequest{BindingId: dt.BindingId})
			if te, ok := asTargetError(err); ok {
				if te.notFound {
					te = &targetError{code: ErrorCodeTargetUnavailable, message: "the binding is not a target of this workspace"}
				}
				h.audited(ctx, ev.Failed(audit.ReasonValidation))
				return UpdateWorkspace422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
			}
			if err != nil {
				return nil, err
			}
			newDefault, defaultBinding = true, target.BindingID
			if dt.Schema != nil && *dt.Schema != "" {
				defaultSchema = dt.Schema
			}
		}
	}

	edit := wsmanage.Edit{Name: request.Body.Name, Description: request.Body.Description}
	if newDefault || clearDefault {
		edit.Default = &wsmanage.Default{Binding: defaultBinding, Schema: defaultSchema}
	}
	workspace, err := h.workspaces.Update(ctx, current, edit)
	if err != nil {
		return nil, err
	}

	ev.Changes = workspaceChanges(current, workspace, newDefault || clearDefault)
	h.audited(ctx, ev.Allowed())
	return UpdateWorkspace200JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

// ListWorkspaceCatalogAccess lists the catalogs granted to a workspace. Any
// member may read them.
func (h *WorkspaceHandlers) ListWorkspaceCatalogAccess(ctx context.Context, request ListWorkspaceCatalogAccessRequestObject) (ListWorkspaceCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListWorkspaceCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListWorkspaceCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	rows, err := h.workspaces.Grants(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceCatalogAccess, 0, len(rows))
	for _, r := range rows {
		name := r.CatalogName
		out = append(out, WorkspaceCatalogAccess{
			CatalogId:   openapi_types.UUID(r.CatalogID.Bytes),
			CatalogName: &name,
			Permission:  WorkspaceCatalogAccessPermission(r.Permission),
			GrantedAt:   pgTimestampToTime(r.GrantedAt),
		})
	}
	return ListWorkspaceCatalogAccess200JSONResponse(out), nil
}

// GrantCatalogAccess grants a catalog to a workspace. Both sides must agree:
// the caller must own (or administer) the workspace and own (or administer) the
// catalog's connection. Both checks run before anything is written.
func (h *WorkspaceHandlers) GrantCatalogAccess(ctx context.Context, request GrantCatalogAccessRequestObject) (GrantCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GrantCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.GrantCreate, ResourceType: "grant", ResourceID: idString(pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); err != nil {
		return grantAccessError(err)
	}
	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	cat, _, err := h.authz().Catalog(ctx, p, catalogID, authz.ActAdmin)
	if err != nil {
		return grantAccessError(err)
	}
	grant, err := h.workspaces.Grant(ctx, wsID, cat.ID, string(request.Body.Permission), p.UserID)
	if errors.Is(err, wsmanage.ErrInvalidPermission) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return GrantCatalogAccess422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    ErrorCodeValidationError,
				Message: err.Error(),
				Errors: []struct {
					Field   *string `json:"field,omitempty"`
					Message *string `json:"message,omitempty"`
				}{{fieldPtr("permission"), msgPtr("must be read or write")}},
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	ev.Changes = map[string]audit.Change{"permission": {To: grant.Permission}}
	h.audited(ctx, ev.Allowed())

	name := cat.TrinoName()
	if name == "" {
		name = cat.Name // a native catalog has no Trino name
	}
	return GrantCatalogAccess201JSONResponse(WorkspaceCatalogAccess{
		CatalogId:   openapi_types.UUID(grant.CatalogID.Bytes),
		CatalogName: &name,
		Permission:  WorkspaceCatalogAccessPermission(grant.Permission),
		GrantedAt:   pgTimestampToTime(grant.GrantedAt),
	}), nil
}

func grantAccessError(err error) (GrantCatalogAccessResponseObject, error) {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return GrantCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace or catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GrantCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to grant this catalog to this workspace"}}, nil
	}
	return nil, err
}

// RevokeCatalogAccess removes a catalog grant. It needs the same two agreeing
// parties as granting.
func (h *WorkspaceHandlers) RevokeCatalogAccess(ctx context.Context, request RevokeCatalogAccessRequestObject) (RevokeCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return RevokeCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.GrantRevoke, ResourceType: "grant", ResourceID: idString(catalogID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); err != nil {
		return revokeAccessError(err)
	}
	if _, _, err := h.authz().Catalog(ctx, p, catalogID, authz.ActAdmin); err != nil {
		return revokeAccessError(err)
	}
	err = h.workspaces.Revoke(ctx, wsID, catalogID)
	if errors.Is(err, wsmanage.ErrGrantNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return RevokeCatalogAccess204Response{}, nil
}

func revokeAccessError(err error) (RevokeCatalogAccessResponseObject, error) {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace or catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return RevokeCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to revoke this catalog from this workspace"}}, nil
	}
	return nil, err
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbWorkspaceToPublic(w db.Workspace) Workspace {
	id := openapi_types.UUID(w.ID.Bytes)
	ws := Workspace{
		Id:                 id,
		Name:               w.Name,
		Type:               WorkspaceType(w.Type),
		PolarisCatalogName: &w.PolarisCatalogName,
		CreatedAt:          pgTimestampToTime(w.CreatedAt),
		UpdatedAt:          func() *time.Time { t := pgTimestampToTime(w.UpdatedAt); return &t }(),
	}
	if w.Description != nil {
		ws.Description = w.Description
	}
	if w.OwnerUserID.Valid {
		uid := openapi_types.UUID(w.OwnerUserID.Bytes)
		ws.OwnerUserId = &uid
	}
	if w.OwnerTeamID.Valid {
		tid := openapi_types.UUID(w.OwnerTeamID.Bytes)
		ws.OwnerTeamId = &tid
	}
	if w.DefaultBindingID.Valid {
		bid := openapi_types.UUID(w.DefaultBindingID.Bytes)
		ws.DefaultBindingId = &bid
		ws.DefaultSchema = w.DefaultSchema
	}
	return ws
}

func fieldPtr(s string) *string { return &s }
func msgPtr(s string) *string   { return &s }

// DeleteWorkspace — DELETE /workspaces/{id} (#108). Shared workspaces only,
// owner or admin, and never while an execution is running.
func (h *WorkspaceHandlers) DeleteWorkspace(ctx context.Context, request DeleteWorkspaceRequestObject) (DeleteWorkspaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.WorkspaceDelete, ResourceType: "workspace", ResourceID: idString(wsID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)
	ws, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DeleteWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DeleteWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace owner may delete it"}}, nil
	case err != nil:
		return nil, err
	}
	err = h.workspaces.Delete(ctx, ws)
	switch {
	case errors.Is(err, wsmanage.ErrPersonal):
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteWorkspace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeWorkspacePersonal, Message: err.Error()}}, nil
	case errors.Is(err, wsmanage.ErrExecutionsRunning):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteWorkspace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeExecutionsRunning, Message: err.Error()}}, nil
	case errors.Is(err, wsmanage.ErrNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteWorkspace204Response{}, nil
}

// GetWorkspaceImpact — GET /workspaces/{id}/impact (#108).
func (h *WorkspaceHandlers) GetWorkspaceImpact(ctx context.Context, request GetWorkspaceImpactRequestObject) (GetWorkspaceImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetWorkspaceImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetWorkspaceImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.workspaces.Impact(ctx, wsID)
	if err != nil {
		return nil, err
	}
	return GetWorkspaceImpact200JSONResponse(WorkspaceImpact{
		SavedQueries: int(row.SavedQueries), Metrics: int(row.Metrics), Spaces: int(row.Spaces),
		RunningExecutions: int(row.RunningExecutions), DefaultTargetAffected: row.DefaultTargetAffected,
	}), nil
}

// GetCatalogAccessImpact — GET /workspaces/{id}/catalog-access/{catalog_id}/impact (#108).
func (h *WorkspaceHandlers) GetCatalogAccessImpact(ctx context.Context, request GetCatalogAccessImpactRequestObject) (GetCatalogAccessImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogAccessImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogAccessImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	if _, _, err := h.authz().Catalog(ctx, p, catalogID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogAccessImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.workspaces.CatalogAccessImpact(ctx, wsID, catalogID)
	if err != nil {
		return nil, err
	}
	return GetCatalogAccessImpact200JSONResponse(WorkspaceImpact{
		SavedQueries: int(row.SavedQueries), Metrics: int(row.Metrics),
		RunningExecutions: int(row.RunningExecutions), DefaultTargetAffected: row.DefaultTargetAffected,
	}), nil
}
