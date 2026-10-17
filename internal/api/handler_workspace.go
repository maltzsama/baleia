package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *Handlers) ListWorkspaces(ctx context.Context, request ListWorkspacesRequestObject) (ListWorkspacesResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return ListWorkspaces401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	workspaces, err := h.queries.ListWorkspacesByUser(ctx, uid)
	if err != nil {
		return nil, err
	}

	result := make([]Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		result = append(result, h.withWorkspaceActions(ctx, w))
	}
	return ListWorkspaces200JSONResponse(result), nil
}

func (h *Handlers) CreateWorkspace(ctx context.Context, request CreateWorkspaceRequestObject) (CreateWorkspaceResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	role := auth.RoleFromContext(ctx)

	var ownerUserID pgtype.UUID
	var ownerTeamID pgtype.UUID

	wsType := string(request.Body.Type)

	if wsType == "shared" {
		if role != "admin" {
			return CreateWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only for shared workspaces"}}, nil
		}
		if request.Body.OwnerTeamId == nil {
			return CreateWorkspace422JSONResponse{
				UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
					Code:    "validation_error",
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

	polarisCatalogName := toPolarisName(request.Body.Name)

	var desc *string
	if request.Body.Description != nil {
		desc = request.Body.Description
	}

	workspace, err := h.queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               request.Body.Name,
		Description:        desc,
		Type:               wsType,
		PolarisCatalogName: polarisCatalogName,
		OwnerUserID:        ownerUserID,
		OwnerTeamID:        ownerTeamID,
	})
	if err != nil {
		return nil, err
	}

	return CreateWorkspace201JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

func (h *Handlers) GetWorkspace(ctx context.Context, request GetWorkspaceRequestObject) (GetWorkspaceResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	workspace, err := h.queries.GetWorkspaceByID(ctx, uid)
	if err != nil {
		return GetWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

	return GetWorkspace200JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

func (h *Handlers) UpdateWorkspace(ctx context.Context, request UpdateWorkspaceRequestObject) (UpdateWorkspaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	uid := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.WorkspaceUpdate, ResourceType: "workspace", ResourceID: idString(uid), WorkspaceID: idString(uid)}
	ctx = audit.WithAction(ctx, ev.Action)
	current, err := h.authz().Workspace(ctx, p, uid, authz.ActWrite)
	if err != nil {
		switch {
		case errors.Is(err, authz.ErrNotFound):
			return UpdateWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
		case errors.Is(err, authz.ErrForbidden):
			return UpdateWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "only a workspace owner may change it"}}, nil
		}
		return nil, err
	}

	name := current.Name
	desc := current.Description
	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Description != nil {
		desc = request.Body.Description
	}

	// A new default target must be a usable target of this workspace.
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
					te = &targetError{code: "target_unavailable", message: "the binding is not a target of this workspace"}
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

	var workspace db.Workspace
	err = h.inTx(ctx, func(q db.Querier) error {
		var err error
		workspace, err = q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
			ID:          uid,
			Name:        name,
			Description: desc,
		})
		if err != nil || (!newDefault && !clearDefault) {
			return err
		}
		if err := q.SetWorkspaceDefaultTarget(ctx, db.SetWorkspaceDefaultTargetParams{ID: uid, DefaultBindingID: defaultBinding, DefaultSchema: defaultSchema}); err != nil {
			return err
		}
		workspace.DefaultBindingID, workspace.DefaultSchema = defaultBinding, defaultSchema
		return nil
	})
	if err != nil {
		return nil, err
	}

	ev.Changes = workspaceChanges(current, workspace, newDefault || clearDefault)
	h.audited(ctx, ev.Allowed())
	return UpdateWorkspace200JSONResponse(h.withWorkspaceActions(ctx, workspace)), nil
}

// ListWorkspaceCatalogAccess lists the catalogs granted to a workspace. Any
// member may read them.
func (h *Handlers) ListWorkspaceCatalogAccess(ctx context.Context, request ListWorkspaceCatalogAccessRequestObject) (ListWorkspaceCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListWorkspaceCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListWorkspaceCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	rows, err := h.queries.ListWorkspaceCatalogGrants(ctx, wsID)
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
func (h *Handlers) GrantCatalogAccess(ctx context.Context, request GrantCatalogAccessRequestObject) (GrantCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GrantCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
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
	if request.Body.Permission != GrantCatalogAccessRequestPermissionRead && request.Body.Permission != GrantCatalogAccessRequestPermissionWrite {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return GrantCatalogAccess422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "permission must be read or write",
				Errors: []struct {
					Field   *string `json:"field,omitempty"`
					Message *string `json:"message,omitempty"`
				}{{fieldPtr("permission"), msgPtr("must be read or write")}},
			},
		}, nil
	}

	grant, err := h.queries.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: wsID,
		CatalogID:   cat.ID,
		Permission:  string(request.Body.Permission),
		GrantedBy:   p.UserID,
	})
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
		return GrantCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace or catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GrantCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to grant this catalog to this workspace"}}, nil
	}
	return nil, err
}

// RevokeCatalogAccess removes a catalog grant. It needs the same two agreeing
// parties as granting.
func (h *Handlers) RevokeCatalogAccess(ctx context.Context, request RevokeCatalogAccessRequestObject) (RevokeCatalogAccessResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return RevokeCatalogAccess401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
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
	n, err := h.queries.DeleteWorkspaceCatalogGrant(ctx, db.DeleteWorkspaceCatalogGrantParams{WorkspaceID: wsID, CatalogID: catalogID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "grant not found"}}, nil
	}
	if err := h.queries.ClearWorkspaceDefaultForCatalog(ctx, db.ClearWorkspaceDefaultForCatalogParams{
		WorkspaceID: wsID, CatalogID: catalogID,
	}); err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return RevokeCatalogAccess204Response{}, nil
}

func revokeAccessError(err error) (RevokeCatalogAccessResponseObject, error) {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace or catalog not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return RevokeCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to revoke this catalog from this workspace"}}, nil
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

// toPolarisName
// Ex: "My Workspace" → "my_workspace_1234"
func toPolarisName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	suffix := fmt.Sprintf("_%d", time.Now().UnixMilli()%10000)
	return b.String() + suffix
}

func fieldPtr(s string) *string { return &s }
func msgPtr(s string) *string   { return &s }

// DeleteWorkspace — DELETE /workspaces/{id} (#108). Shared workspaces only,
// owner or admin, and never while an execution is running.
func (h *Handlers) DeleteWorkspace(ctx context.Context, request DeleteWorkspaceRequestObject) (DeleteWorkspaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.WorkspaceDelete, ResourceType: "workspace", ResourceID: idString(wsID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)
	ws, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DeleteWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DeleteWorkspace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "only a workspace owner may delete it"}}, nil
	case err != nil:
		return nil, err
	}
	if ws.Type == "personal" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteWorkspace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "workspace_personal", Message: "personal workspaces cannot be deleted"}}, nil
	}
	running, err := h.queries.CountWorkspaceRunningExecutions(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if running > 0 {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteWorkspace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "executions_running", Message: "wait for the workspace's running executions to finish"}}, nil
	}
	n, err := h.queries.DeleteWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteWorkspace204Response{}, nil
}

// GetWorkspaceImpact — GET /workspaces/{id}/impact (#108).
func (h *Handlers) GetWorkspaceImpact(ctx context.Context, request GetWorkspaceImpactRequestObject) (GetWorkspaceImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetWorkspaceImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetWorkspaceImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.queries.CountWorkspaceImpact(ctx, wsID)
	if err != nil {
		return nil, err
	}
	return GetWorkspaceImpact200JSONResponse(WorkspaceImpact{
		SavedQueries: int(row.SavedQueries), Metrics: int(row.Metrics), Spaces: int(row.Spaces),
		RunningExecutions: int(row.RunningExecutions), DefaultTargetAffected: row.DefaultTargetAffected,
	}), nil
}

// GetCatalogAccessImpact — GET /workspaces/{id}/catalog-access/{catalog_id}/impact (#108).
func (h *Handlers) GetCatalogAccessImpact(ctx context.Context, request GetCatalogAccessImpactRequestObject) (GetCatalogAccessImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogAccessImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogAccessImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	if _, _, err := h.authz().Catalog(ctx, p, catalogID, authz.ActAdmin); errors.Is(err, authz.ErrNotFound) {
		return GetCatalogAccessImpact404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.queries.CountCatalogAccessImpact(ctx, db.CountCatalogAccessImpactParams{WorkspaceID: wsID, CatalogID: catalogID})
	if err != nil {
		return nil, err
	}
	return GetCatalogAccessImpact200JSONResponse(WorkspaceImpact{
		SavedQueries: int(row.SavedQueries), Metrics: int(row.Metrics),
		RunningExecutions: int(row.RunningExecutions), DefaultTargetAffected: row.DefaultTargetAffected,
	}), nil
}
