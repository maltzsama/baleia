package api

import (
    "context"
    "fmt"
    "strings"
    "time"
    "unicode"

    "github.com/galileostudio/baleia/internal/auth"
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
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return UpdateWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	current, err := h.queries.GetWorkspaceByID(ctx, uid)
	if err != nil {
		return UpdateWorkspace404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
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

func (h *Handlers) ListWorkspaceCatalogAccess(ctx context.Context, request ListWorkspaceCatalogAccessRequestObject) (ListWorkspaceCatalogAccessResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListWorkspaceCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetWorkspaceByID(ctx, uid)
	if err != nil {
		return ListWorkspaceCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

    accesses, err := h.queries.ListCatalogAccessByWorkspace(ctx, uid)
    if err != nil {
        return nil, err
    }

    result := make([]WorkspaceCatalogAccess, 0, len(accesses))
    for _, a := range accesses {
        catalogID := openapi_types.UUID(a.CatalogConnectionID.Bytes)
        catalogType := WorkspaceCatalogAccessCatalogType(a.CatalogType)
        result = append(result, WorkspaceCatalogAccess{
            CatalogConnectionId: catalogID,
            CatalogName:         &a.CatalogName,
            CatalogType:         &catalogType,
            Permission:          WorkspaceCatalogAccessPermission(a.Permission),
            GrantedAt:           pgTimestampToTime(a.GrantedAt),
        })
    }
    return ListWorkspaceCatalogAccess200JSONResponse(result), nil
}

func (h *Handlers) GrantCatalogAccess(ctx context.Context, request GrantCatalogAccessRequestObject) (GrantCatalogAccessResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return GrantCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GrantCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "workspace not found"}}, nil
	}

    userID := auth.UserIDFromContext(ctx)
    grantedBy, err := uuidFromString(userID)
    if err != nil {
        return nil, err
    }

    var workspaceID pgtype.UUID
    workspaceID.Bytes = request.Id
    workspaceID.Valid = true

    var catalogID pgtype.UUID
    catalogID.Bytes = request.Body.CatalogConnectionId
    catalogID.Valid = true

    access, err := h.queries.GrantCatalogAccess(ctx, db.GrantCatalogAccessParams{
        WorkspaceID:         workspaceID,
        CatalogConnectionID: catalogID,
        Permission:          string(request.Body.Permission),
        GrantedBy:           grantedBy,
    })
    if err != nil {
        return nil, err
    }

    conn, err := h.queries.GetCatalogConnectionByID(ctx, catalogID)
    if err != nil {
        return nil, err
    }

    catalogConnID := openapi_types.UUID(access.CatalogConnectionID.Bytes)
    catalogType := WorkspaceCatalogAccessCatalogType(conn.Type)
    return GrantCatalogAccess201JSONResponse{
        CatalogConnectionId: catalogConnID,
        CatalogName:         &conn.Name,
        CatalogType:         &catalogType,
        Permission:          WorkspaceCatalogAccessPermission(access.Permission),
        GrantedAt:           pgTimestampToTime(access.GrantedAt),
    }, nil
}

func (h *Handlers) RevokeCatalogAccess(ctx context.Context, request RevokeCatalogAccessRequestObject) (RevokeCatalogAccessResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return RevokeCatalogAccess403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}

    var workspaceID pgtype.UUID
    workspaceID.Bytes = request.Id
    workspaceID.Valid = true

    var catalogID pgtype.UUID
    catalogID.Bytes = request.CatalogConnectionId
    catalogID.Valid = true

    if err := h.queries.RevokeCatalogAccess(ctx, db.RevokeCatalogAccessParams{
        WorkspaceID:         workspaceID,
        CatalogConnectionID: catalogID,
    }); err != nil {
        return RevokeCatalogAccess404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "access not found"}}, nil
    }

    return RevokeCatalogAccess204Response{}, nil
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