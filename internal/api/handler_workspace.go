package api

import (
    "context"
    "errors"
    "fmt"
    "strings"
    "time"
    "unicode"

    "github.com/galileostudio/baleia/internal/auth"
    "github.com/galileostudio/baleia/internal/authz"
    "github.com/galileostudio/baleia/internal/db"
    openapi_types "github.com/oapi-codegen/runtime/types"
    "github.com/jackc/pgx/v5/pgtype"
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
        result = append(result, dbWorkspaceToPublic(w))
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
                    Errors:  []struct{ Field *string `json:"field,omitempty"`; Message *string `json:"message,omitempty"` }{{fieldPtr("owner_team_id"), msgPtr("required")}},
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

    return CreateWorkspace201JSONResponse(dbWorkspaceToPublic(workspace)), nil
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

	return GetWorkspace200JSONResponse(dbWorkspaceToPublic(workspace)), nil
}

func (h *Handlers) UpdateWorkspace(ctx context.Context, request UpdateWorkspaceRequestObject) (UpdateWorkspaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	uid := workspaceIDFromRequest(request.Id)
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

	workspace, err := h.queries.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
		ID:          uid,
		Name:        name,
		Description: desc,
	})
	if err != nil {
		return nil, err
	}

	return UpdateWorkspace200JSONResponse(dbWorkspaceToPublic(workspace)), nil
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
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin); err != nil {
		return grantAccessError(err)
	}
	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	cat, _, err := h.authz().Catalog(ctx, p, catalogID, authz.ActAdmin)
	if err != nil {
		return grantAccessError(err)
	}
	if request.Body.Permission != GrantCatalogAccessRequestPermissionRead && request.Body.Permission != GrantCatalogAccessRequestPermissionWrite {
		return GrantCatalogAccess422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "permission must be read or write",
				Errors:  []struct{ Field *string `json:"field,omitempty"`; Message *string `json:"message,omitempty"` }{{fieldPtr("permission"), msgPtr("must be read or write")}},
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

	name := cat.TrinoCatalogName
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
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "grant not found"}}, nil
	}
	h.catalogCache.Clear()
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
        PolarisCatalogName: w.PolarisCatalogName,
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