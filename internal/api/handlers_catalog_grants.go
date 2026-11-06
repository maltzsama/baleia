package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// The two-party grant flow (ADR 0018 §7.3): a catalog owner offers, a
// workspace admin requests, the other party approves, and someone holding both
// rights grants directly. Pending requests live in catalog_grant_requests; an
// active grant is a row in workspace_catalog_grants.

func grantStr(s string) *string { return &s }

func nilOrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func actionsPtr(s ...string) *[]string { return &s }

// workspaceAdmin reports whether the caller administers the workspace.
func (h *core) workspaceAdmin(ctx context.Context, p authz.Principal, wsID pgtype.UUID) bool {
	_, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin)
	return err == nil
}

// catalogOwner reports whether the caller owns the catalog's connection (or is
// a global admin): the "catalog" side of a grant.
func (h *core) catalogOwner(ctx context.Context, p authz.Principal, catID pgtype.UUID) bool {
	_, _, err := h.authz().Catalog(ctx, p, catID, authz.ActAdmin)
	return err == nil
}

// activeItem builds the active-grant item for a (workspace, catalog) pair.
func (h *core) activeItem(ctx context.Context, wsID, catID pgtype.UUID) CatalogGrantItem {
	item := CatalogGrantItem{
		WorkspaceId:    openapi_types.UUID(wsID.Bytes),
		CatalogId:      openapi_types.UUID(catID.Bytes),
		State:          CatalogGrantItemStateActive,
		AllowedActions: actionsPtr("unlink"),
	}
	if cat, err := h.queries.GetCatalogByID(ctx, catID); err == nil {
		item.CatalogName = grantStr(cat.Name)
		if cat.TrinoCatalogName != nil && *cat.TrinoCatalogName != "" {
			item.EngineCatalogName = cat.TrinoCatalogName
		}
	}
	if w, err := h.queries.GetWorkspaceByID(ctx, wsID); err == nil {
		item.WorkspaceName = grantStr(w.Name)
	}
	return item
}

// ListCatalogGrantItems — GET /catalog-grants. Exactly one of workspace_id or
// catalog_id selects the side the caller manages; the result is the active
// grants and the pending requests on that side.
func (h *CatalogHandlers) ListCatalogGrantItems(ctx context.Context, request ListCatalogGrantItemsRequestObject) (ListCatalogGrantItemsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogGrantItems401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	params := request.Params
	if (params.WorkspaceId == nil) == (params.CatalogId == nil) {
		return ListCatalogGrantItems404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "exactly one of workspace_id or catalog_id is required"}}, nil
	}

	out := make([]CatalogGrantItem, 0, 4)
	if params.WorkspaceId != nil {
		wsID := pgtype.UUID{Bytes: *params.WorkspaceId, Valid: true}
		if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActRead); err != nil {
			return ListCatalogGrantItems404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		grants, err := h.queries.ListWorkspaceCatalogGrants(ctx, wsID)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			item := CatalogGrantItem{
				WorkspaceId:    openapi_types.UUID(wsID.Bytes),
				CatalogId:      openapi_types.UUID(g.CatalogID.Bytes),
				State:          CatalogGrantItemStateActive,
				CatalogName:    grantStr(g.CatalogName),
				AllowedActions: actionsPtr("unlink"),
			}
			if g.GrantedAt.Valid {
				t := g.GrantedAt.Time
				item.GrantedAt = &t
			}
			out = append(out, item)
		}
		pending, err := h.queries.ListCatalogGrantRequestsForWorkspace(ctx, wsID)
		if err != nil {
			return nil, err
		}
		for _, r := range pending {
			out = append(out, requestItem(r.CatalogID, r.InitiatedBySide, r.RequestedBy, r.RequestedAt, r.CatalogName, r.EngineCatalogName, r.ConnectionName))
		}
	} else {
		catID := pgtype.UUID{Bytes: *params.CatalogId, Valid: true}
		if _, err := h.queries.GetCatalogByID(ctx, catID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ListCatalogGrantItems404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
			}
			return nil, err
		}
		if !h.catalogOwner(ctx, p, catID) {
			return ListCatalogGrantItems404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		}
		grants, err := h.queries.ListCatalogGrants(ctx, catID)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			item := CatalogGrantItem{
				WorkspaceId:    openapi_types.UUID(g.WorkspaceID.Bytes),
				CatalogId:      openapi_types.UUID(catID.Bytes),
				State:          CatalogGrantItemStateActive,
				WorkspaceName:  grantStr(g.WorkspaceName),
				AllowedActions: actionsPtr("unlink"),
			}
			if g.GrantedAt.Valid {
				t := g.GrantedAt.Time
				item.GrantedAt = &t
			}
			out = append(out, item)
		}
		pending, err := h.queries.ListCatalogGrantRequestsForCatalog(ctx, catID)
		if err != nil {
			return nil, err
		}
		for _, r := range pending {
			out = append(out, requestItem(r.CatalogID, r.InitiatedBySide, r.RequestedBy, r.RequestedAt, "", "", ""))
			last := &out[len(out)-1]
			last.WorkspaceName = grantStr(r.WorkspaceName)
		}
	}
	return ListCatalogGrantItems200JSONResponse(out), nil
}

// requestItem builds a pending-request item (the workspace side fills the
// catalog/connection names).
func requestItem(catID pgtype.UUID, side string, requestedBy pgtype.UUID, requestedAt pgtype.Timestamptz, catalogName, engineName, connName string) CatalogGrantItem {
	s := CatalogGrantItemInitiatedBySide(side)
	item := CatalogGrantItem{
		CatalogId:         openapi_types.UUID(catID.Bytes),
		State:             CatalogGrantItemStatePending,
		InitiatedBySide:   &s,
		AllowedActions:    actionsPtr("approve", "decline"),
		CatalogName:       grantStr(catalogName),
		EngineCatalogName: nilOrStr(engineName),
		ConnectionName:    nilOrStr(connName),
	}
	if requestedBy.Valid {
		u := openapi_types.UUID(requestedBy.Bytes)
		item.RequestedBy = &u
	}
	if requestedAt.Valid {
		t := requestedAt.Time
		item.RequestedAt = &t
	}
	return item
}

// CreateCatalogGrant — POST /catalog-grants. The caller's rights decide the
// outcome: both rights are a direct grant, only the catalog side is an offer,
// only the workspace side is a request.
func (h *CatalogHandlers) CreateCatalogGrant(ctx context.Context, request CreateCatalogGrantRequestObject) (CreateCatalogGrantResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateCatalogGrant401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return CreateCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "missing body"}}, nil
	}
	wsID := pgtype.UUID{Bytes: request.Body.WorkspaceId, Valid: true}
	catID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.GrantCreate, ResourceType: "grant", ResourceID: idString(catID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)

	wsAdmin := h.workspaceAdmin(ctx, p, wsID)
	catOwner := h.catalogOwner(ctx, p, catID)
	if !wsAdmin && !catOwner {
		return CreateCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace or catalog not found"}}, nil
	}

	if wsAdmin && catOwner {
		if _, err := h.queries.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{WorkspaceID: wsID, CatalogID: catID, GrantedBy: p.UserID}); err != nil {
			return nil, err
		}
		h.catalogCache.Clear()
		h.audited(ctx, ev.Allowed())
		return CreateCatalogGrant200JSONResponse(h.activeItem(ctx, wsID, catID)), nil
	}

	side := "workspace"
	if catOwner {
		side = "catalog"
	}
	if _, err := h.queries.GetCatalogByID(ctx, catID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		}
		return nil, err
	}
	req, err := h.queries.CreateCatalogGrantRequest(ctx, db.CreateCatalogGrantRequestParams{
		WorkspaceID: wsID, CatalogID: catID, InitiatedBySide: side, RequestedBy: p.UserID,
	})
	if err != nil {
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return CreateCatalogGrant200JSONResponse(requestItem(catID, req.InitiatedBySide, req.RequestedBy, req.RequestedAt, "", "", "")), nil
}

// ApproveCatalogGrant — POST /catalog-grants/{workspace_id}/{catalog_id}/approve.
// The other party turns a pending request into an active grant in one
// transaction.
func (h *CatalogHandlers) ApproveCatalogGrant(ctx context.Context, request ApproveCatalogGrantRequestObject) (ApproveCatalogGrantResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ApproveCatalogGrant401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := pgtype.UUID{Bytes: request.WorkspaceId, Valid: true}
	catID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.GrantCreate, ResourceType: "grant", ResourceID: idString(catID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)

	req, err := h.queries.GetCatalogGrantRequest(ctx, db.GetCatalogGrantRequestParams{WorkspaceID: wsID, CatalogID: catID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ApproveCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "no pending request"}}, nil
		}
		return nil, err
	}
	if req.InitiatedBySide == "catalog" {
		if !h.workspaceAdmin(ctx, p, wsID) {
			return ApproveCatalogGrant403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace admin may approve this offer"}}, nil
		}
	} else {
		if !h.catalogOwner(ctx, p, catID) {
			return ApproveCatalogGrant403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the catalog owner may approve this request"}}, nil
		}
	}

	txErr := h.inTx(ctx, func(q db.Querier) error {
		if _, err := q.DeleteCatalogGrantRequest(ctx, db.DeleteCatalogGrantRequestParams{WorkspaceID: wsID, CatalogID: catID}); err != nil {
			return err
		}
		_, err := q.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{WorkspaceID: wsID, CatalogID: catID, GrantedBy: p.UserID})
		return err
	})
	if txErr != nil {
		return nil, txErr
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return ApproveCatalogGrant200JSONResponse(h.activeItem(ctx, wsID, catID)), nil
}

// DeleteCatalogGrant — DELETE /catalog-grants/{workspace_id}/{catalog_id}.
// Declines a pending request or unlinks an active grant.
func (h *CatalogHandlers) DeleteCatalogGrant(ctx context.Context, request DeleteCatalogGrantRequestObject) (DeleteCatalogGrantResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogGrant401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := pgtype.UUID{Bytes: request.WorkspaceId, Valid: true}
	catID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.GrantRevoke, ResourceType: "grant", ResourceID: idString(catID), WorkspaceID: idString(wsID)}
	ctx = audit.WithAction(ctx, ev.Action)

	if !h.workspaceAdmin(ctx, p, wsID) && !h.catalogOwner(ctx, p, catID) {
		return DeleteCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace or catalog not found"}}, nil
	}

	found := false
	if n, err := h.queries.DeleteCatalogGrantRequest(ctx, db.DeleteCatalogGrantRequestParams{WorkspaceID: wsID, CatalogID: catID}); err != nil {
		return nil, err
	} else if n > 0 {
		found = true
	}
	if n, err := h.queries.DeleteWorkspaceCatalogGrant(ctx, db.DeleteWorkspaceCatalogGrantParams{WorkspaceID: wsID, CatalogID: catID}); err != nil {
		return nil, err
	} else if n > 0 {
		found = true
	}
	if !found {
		return DeleteCatalogGrant404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "no grant or request"}}, nil
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalogGrant204Response{}, nil
}

// ListGrantableWorkspaces — GET /catalogs/{catalog_id}/grantable-workspaces.
func (h *CatalogHandlers) ListGrantableWorkspaces(ctx context.Context, request ListGrantableWorkspacesRequestObject) (ListGrantableWorkspacesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListGrantableWorkspaces401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if !h.catalogOwner(ctx, p, catID) {
		return ListGrantableWorkspaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	rows, err := h.queries.ListGrantableWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GrantableWorkspace, 0, len(rows))
	for _, w := range rows {
		out = append(out, GrantableWorkspace{Id: openapi_types.UUID(w.ID.Bytes), Name: w.Name, Type: w.Type, TeamName: w.TeamName})
	}
	return ListGrantableWorkspaces200JSONResponse(out), nil
}

// SetCatalogDiscoverable — PATCH /catalogs/{catalog_id}/discoverable.
func (h *CatalogHandlers) SetCatalogDiscoverable(ctx context.Context, request SetCatalogDiscoverableRequestObject) (SetCatalogDiscoverableResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SetCatalogDiscoverable401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return SetCatalogDiscoverable404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "missing body"}}, nil
	}
	catID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if !h.catalogOwner(ctx, p, catID) {
		return SetCatalogDiscoverable404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	if err := h.queries.SetCatalogDiscoverable(ctx, db.SetCatalogDiscoverableParams{ID: catID, Discoverable: request.Body.Discoverable}); err != nil {
		return nil, err
	}
	cat, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return nil, err
	}
	return SetCatalogDiscoverable200JSONResponse(h.catalogWithStatus(ctx, cat)), nil
}

// ListRequestableCatalogs — GET /workspaces/{id}/requestable-catalogs.
func (h *CatalogHandlers) ListRequestableCatalogs(ctx context.Context, request ListRequestableCatalogsRequestObject) (ListRequestableCatalogsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListRequestableCatalogs401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if !h.workspaceAdmin(ctx, p, wsID) {
		return ListRequestableCatalogs404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	}
	rows, err := h.queries.ListDiscoverableCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RequestableCatalog, 0, len(rows))
	for _, c := range rows {
		out = append(out, RequestableCatalog{Id: openapi_types.UUID(c.ID.Bytes), Name: c.Name, ConnectionName: c.ConnectionName, Description: c.Description})
	}
	return ListRequestableCatalogs200JSONResponse(out), nil
}
