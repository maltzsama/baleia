package api

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Spaces (#40). The effective role is computed in SQL
// (baleia_effective_space_role, internal/authz.RequireSpace): a space the
// caller cannot see is a 404, never a 403, and a visible space below the
// required role is a 403.

func spaceIDFromRequest(id openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func spaceError(err error) (spaceErr, bool) {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return spaceErr{status: 404}, true
	case errors.Is(err, authz.ErrForbidden):
		return spaceErr{status: 403}, true
	}
	return spaceErr{}, false
}

type spaceErr struct{ status int }

// requireSpace maps RequireSpace to (role, ok, handled status).
func (h *Handlers) requireSpace(ctx context.Context, ws, spaceID pgtype.UUID, min authz.SpaceRole) (authz.SpaceRole, *spaceErr, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return authz.SpaceNone, nil, authz.ErrUnauthenticated
	}
	role, err := h.authz().RequireSpace(ctx, p, ws, spaceID, min)
	if err != nil {
		if se, ok := spaceError(err); ok {
			return authz.SpaceNone, &se, nil
		}
		return authz.SpaceNone, nil, err
	}
	return role, nil, nil
}

// contentSpace is requireSpace for a metric: it returns nil when allowed, a
// spaceErr with 404 (invisible) or 403 (below min), or a real error.
func (h *Handlers) contentSpace(ctx context.Context, ws, spaceID pgtype.UUID, min authz.SpaceRole) (*spaceErr, error) {
	_, se, err := h.requireSpace(ctx, ws, spaceID, min)
	return se, err
}

// spaceForWrite resolves the space a write targets: the requested one, else the
// workspace's default space, and requires min on it.
func (h *Handlers) spaceForWrite(ctx context.Context, ws pgtype.UUID, requested *openapi_types.UUID, min authz.SpaceRole) (db.Space, *spaceErr, error) {
	var space db.Space
	var err error
	if requested != nil {
		space, err = h.queries.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: spaceIDFromRequest(*requested)})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Space{}, &spaceErr{status: 404}, nil
		}
		if err != nil {
			return db.Space{}, nil, err
		}
	} else {
		space, err = h.queries.GetDefaultSpace(ctx, ws)
		if err != nil {
			return db.Space{}, nil, err
		}
	}
	_, se, err := h.requireSpace(ctx, ws, space.ID, min)
	if err != nil {
		return db.Space{}, nil, err
	}
	if se != nil {
		return db.Space{}, se, nil
	}
	return space, nil, nil
}

// ListSpaces — GET /workspaces/{id}/spaces
func (h *Handlers) ListSpaces(ctx context.Context, request ListSpacesRequestObject) (ListSpacesResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListSpaces401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if _, err := h.authz().Workspace(ctx, p, ws, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListSpaces404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
		}
		return nil, err
	}
	rows, err := h.queries.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	out := make(ListSpaces200JSONResponse, 0, len(rows))
	for _, r := range rows {
		role := r.MyRole
		sp := dbSpaceRowToPublic(r, &role)
		h.decorateSpace(ctx, &sp, role)
		out = append(out, sp)
	}
	return out, nil
}

// CreateSpace — POST /workspaces/{id}/spaces
func (h *Handlers) CreateSpace(ctx context.Context, request CreateSpaceRequestObject) (CreateSpaceResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateSpace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	ws := workspaceIDFromRequest(request.Id)
	ev := audit.Event{Action: audit.SpaceCreate, ResourceType: "space", WorkspaceID: idString(ws)}
	ctx = audit.WithAction(ctx, ev.Action)
	if _, err := h.authz().Workspace(ctx, p, ws, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return CreateSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return nil, errors.New("space: missing body")
	}
	name := strings.TrimSpace(request.Body.Name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return CreateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "name must be 1 to 120 characters")}, nil
	}
	visibility := string(request.Body.Visibility)
	if visibility != "workspace" && visibility != "restricted" {
		return CreateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "visibility must be workspace or restricted")}, nil
	}
	defaultRole := "viewer"
	if request.Body.DefaultRole != nil {
		defaultRole = string(*request.Body.DefaultRole)
	}
	if defaultRole != "viewer" && defaultRole != "editor" {
		return CreateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "default_role must be viewer or editor")}, nil
	}

	slug, err := uniqueSlug(ctx, name, func(ctx context.Context, s string) (bool, error) {
		return h.queries.SpaceSlugExists(ctx, db.SpaceSlugExistsParams{WorkspaceID: ws, Slug: s})
	})
	if err != nil {
		return nil, err
	}

	var space db.Space
	err = h.inTx(ctx, func(q db.Querier) error {
		var err error
		space, err = q.CreateSpace(ctx, db.CreateSpaceParams{
			WorkspaceID: ws,
			Name:        name,
			Slug:        slug,
			Description: request.Body.Description,
			Visibility:  visibility,
			DefaultRole: defaultRole,
			CreatedBy:   p.UserID,
		})
		if err != nil {
			return err
		}
		// The creator is an explicit manager, so a restricted space is not
		// invisible to the person who just made it.
		_, err = q.SetSpaceMember(ctx, db.SetSpaceMemberParams{
			SpaceID: space.ID, PrincipalType: "user", PrincipalID: p.UserID, Role: "manager", GrantedBy: p.UserID,
		})
		return err
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CreateSpace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "a space with this name already exists in this workspace"}}, nil
		}
		return nil, err
	}

	ev.ResourceID = idString(space.ID)
	h.audited(ctx, ev.Allowed())
	return CreateSpace201JSONResponse(dbSpaceToPublic(space, "manager")), nil
}

// GetSpace — GET /workspaces/{id}/spaces/{space_id}
func (h *Handlers) GetSpace(ctx context.Context, request GetSpaceRequestObject) (GetSpaceResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	role, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceViewer)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSpace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		return GetSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	space, err := h.queries.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: spaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	sp := dbSpaceToPublic(space, role.String())
	h.decorateSpace(ctx, &sp, role.String())
	return GetSpace200JSONResponse(sp), nil
}

// UpdateSpace — PATCH /workspaces/{id}/spaces/{space_id}
func (h *Handlers) UpdateSpace(ctx context.Context, request UpdateSpaceRequestObject) (UpdateSpaceResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	ev := audit.Event{Action: audit.SpaceUpdate, ResourceType: "space", ResourceID: idString(spaceID), WorkspaceID: idString(ws)}
	ctx = audit.WithAction(ctx, ev.Action)
	_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceManager)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return UpdateSpace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return UpdateSpace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a space manager may change it"}}, nil
		}
		return UpdateSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("space: missing body")
	}
	current, err := h.queries.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: spaceID})
	if err != nil {
		return nil, err
	}
	name, description := current.Name, current.Description
	visibility, defaultRole := current.Visibility, current.DefaultRole
	if request.Body.Name != nil {
		name = strings.TrimSpace(*request.Body.Name)
	}
	if request.Body.Description != nil {
		description = request.Body.Description
	}
	if request.Body.Visibility != nil {
		visibility = string(*request.Body.Visibility)
	}
	if request.Body.DefaultRole != nil {
		defaultRole = string(*request.Body.DefaultRole)
	}
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "name must be 1 to 120 characters")}, nil
	}
	if visibility != "workspace" && visibility != "restricted" {
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "visibility must be workspace or restricted")}, nil
	}
	if defaultRole != "viewer" && defaultRole != "editor" {
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "default_role must be viewer or editor")}, nil
	}
	if current.IsDefault && visibility != "workspace" {
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "the default space must stay workspace-visible")}, nil
	}
	space, err := h.queries.UpdateSpace(ctx, db.UpdateSpaceParams{
		WorkspaceID: ws, ID: spaceID, Name: name, Description: description,
		Visibility: visibility, DefaultRole: defaultRole,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeConflict, "a space with this name already exists in this workspace")}, nil
		}
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return UpdateSpace200JSONResponse(dbSpaceToPublic(space, "manager")), nil
}

// DeleteSpace — DELETE /workspaces/{id}/spaces/{space_id}
func (h *Handlers) DeleteSpace(ctx context.Context, request DeleteSpaceRequestObject) (DeleteSpaceResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	ev := audit.Event{Action: audit.SpaceDelete, ResourceType: "space", ResourceID: idString(spaceID), WorkspaceID: idString(ws)}
	ctx = audit.WithAction(ctx, ev.Action)
	_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceManager)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSpace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return DeleteSpace403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a space manager may delete it"}}, nil
		}
		return DeleteSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	current, err := h.queries.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: spaceID})
	if err != nil {
		return nil, err
	}
	if current.IsDefault {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteSpace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "the default space cannot be deleted"}}, nil
	}
	n, err := h.queries.CountSpaceContent(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	force := request.Params.Force != nil && *request.Params.Force
	if n > 0 && !force {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteSpace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "the space is not empty; pass force=true to delete it and its content"}}, nil
	}
	deleted, err := h.queries.DeleteSpace(ctx, db.DeleteSpaceParams{WorkspaceID: ws, ID: spaceID})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	h.audited(ctx, ev.Allowed())
	return DeleteSpace204Response{}, nil
}

// ListSpaceMembers — GET /workspaces/{id}/spaces/{space_id}/members
func (h *Handlers) ListSpaceMembers(ctx context.Context, request ListSpaceMembersRequestObject) (ListSpaceMembersResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceViewer)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return ListSpaceMembers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return ListSpaceMembers403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return ListSpaceMembers404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	rows, err := h.queries.ListSpaceMembers(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	out := make(ListSpaceMembers200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbSpaceMemberToPublic(r))
	}
	return out, nil
}

// SetSpaceMember — PUT /workspaces/{id}/spaces/{space_id}/members/{principal_type}/{principal_id}
func (h *Handlers) SetSpaceMember(ctx context.Context, request SetSpaceMemberRequestObject) (SetSpaceMemberResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	principalID := spaceIDFromRequest(request.PrincipalId)
	ev := audit.Event{Action: audit.SpaceMemberSet, ResourceType: "space", ResourceID: idString(spaceID), WorkspaceID: idString(ws)}
	ctx = audit.WithAction(ctx, ev.Action)
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SetSpaceMember401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceManager)
	if err != nil {
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return SetSpaceMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a space manager may change its access list"}}, nil
		}
		return SetSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("space member: missing body")
	}
	principalType := request.PrincipalType
	switch principalType {
	case "user":
		if _, err := h.queries.GetUserByID(ctx, principalID); err != nil {
			return SetSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound("user not found")}, nil
		}
	case "team":
		if _, err := h.queries.GetTeamByID(ctx, principalID); err != nil {
			return SetSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound("team not found")}, nil
		}
	default:
		return SetSpaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "principal_type must be user or team")}, nil
	}
	role := string(request.Body.Role)
	if role != "viewer" && role != "editor" && role != "manager" {
		return SetSpaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "role must be viewer, editor or manager")}, nil
	}
	member, err := h.queries.SetSpaceMember(ctx, db.SetSpaceMemberParams{
		SpaceID: spaceID, PrincipalType: principalType, PrincipalID: principalID, Role: role, GrantedBy: p.UserID,
	})
	if err != nil {
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return SetSpaceMember200JSONResponse(dbSpaceMemberToPublic(member)), nil
}

// DeleteSpaceMember — DELETE /workspaces/{id}/spaces/{space_id}/members/{principal_type}/{principal_id}
func (h *Handlers) DeleteSpaceMember(ctx context.Context, request DeleteSpaceMemberRequestObject) (DeleteSpaceMemberResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	principalID := spaceIDFromRequest(request.PrincipalId)
	ev := audit.Event{Action: audit.SpaceMemberRemove, ResourceType: "space", ResourceID: idString(spaceID), WorkspaceID: idString(ws)}
	ctx = audit.WithAction(ctx, ev.Action)
	_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceManager)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSpaceMember401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return DeleteSpaceMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a space manager may change its access list"}}, nil
		}
		return DeleteSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	deleted, err := h.queries.DeleteSpaceMember(ctx, db.DeleteSpaceMemberParams{
		SpaceID: spaceID, PrincipalType: request.PrincipalType, PrincipalID: principalID,
	})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound("space member not found")}, nil
	}
	h.audited(ctx, ev.Allowed())
	return DeleteSpaceMember204Response{}, nil
}

// ── converters ──────────────────────────────────────────────────────────────────

func dbSpaceToPublic(s db.Space, role string) Space {
	out := Space{
		Id:          openapi_types.UUID(s.ID.Bytes),
		WorkspaceId: openapi_types.UUID(s.WorkspaceID.Bytes),
		Name:        s.Name,
		Slug:        s.Slug,
		Description: s.Description,
		Visibility:  SpaceVisibility(s.Visibility),
		DefaultRole: SpaceDefaultRole(s.DefaultRole),
		IsDefault:   s.IsDefault,
		CreatedAt:   pgTimestampToTime(s.CreatedAt),
		UpdatedAt:   timePtr(s.UpdatedAt),
	}
	if s.CreatedBy.Valid {
		id := openapi_types.UUID(s.CreatedBy.Bytes)
		out.CreatedBy = &id
	}
	if role != "" {
		r := SpaceMyRole(role)
		out.MyRole = &r
	}
	return out
}

func dbSpaceRowToPublic(r db.ListSpacesForPrincipalRow, role *string) Space {
	out := Space{
		Id:          openapi_types.UUID(r.ID.Bytes),
		WorkspaceId: openapi_types.UUID(r.WorkspaceID.Bytes),
		Name:        r.Name,
		Slug:        r.Slug,
		Description: r.Description,
		Visibility:  SpaceVisibility(r.Visibility),
		DefaultRole: SpaceDefaultRole(r.DefaultRole),
		IsDefault:   r.IsDefault,
		CreatedAt:   pgTimestampToTime(r.CreatedAt),
		UpdatedAt:   timePtr(r.UpdatedAt),
	}
	if r.CreatedBy.Valid {
		id := openapi_types.UUID(r.CreatedBy.Bytes)
		out.CreatedBy = &id
	}
	if role != nil && *role != "" {
		my := SpaceMyRole(*role)
		out.MyRole = &my
	}
	return out
}

func dbSpaceMemberToPublic(m db.SpaceMember) SpaceMember {
	out := SpaceMember{
		PrincipalType: SpaceMemberPrincipalType(m.PrincipalType),
		PrincipalId:   openapi_types.UUID(m.PrincipalID.Bytes),
		Role:          SpaceMemberRole(m.Role),
		GrantedAt:     timePtr(m.GrantedAt),
	}
	if m.GrantedBy.Valid {
		id := openapi_types.UUID(m.GrantedBy.Bytes)
		out.GrantedBy = &id
	}
	return out
}
