package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/workspaces/spaces"
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
func (h *core) requireSpace(ctx context.Context, ws, spaceID pgtype.UUID, minRole authz.SpaceRole) (authz.SpaceRole, *spaceErr, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return authz.SpaceNone, nil, authz.ErrUnauthenticated
	}
	role, err := h.authz().RequireSpace(ctx, p, ws, spaceID, minRole)
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
func (h *core) contentSpace(ctx context.Context, ws, spaceID pgtype.UUID, minRole authz.SpaceRole) (*spaceErr, error) {
	_, se, err := h.requireSpace(ctx, ws, spaceID, minRole)
	return se, err
}

// spaceForWrite resolves the space a write targets: the requested one, else the
// workspace's default space, and requires min on it.
func (h *core) spaceForWrite(ctx context.Context, ws pgtype.UUID, requested *openapi_types.UUID, minRole authz.SpaceRole) (db.Space, *spaceErr, error) {
	var space db.Space
	var err error
	if requested != nil {
		space, err = h.spaces.Get(ctx, ws, spaceIDFromRequest(*requested))
		if errors.Is(err, spaces.ErrNotFound) {
			return db.Space{}, &spaceErr{status: 404}, nil
		}
		if err != nil {
			return db.Space{}, nil, err
		}
	} else {
		space, err = h.spaces.Default(ctx, ws)
		if err != nil {
			return db.Space{}, nil, err
		}
	}
	_, se, err := h.requireSpace(ctx, ws, space.ID, minRole)
	if err != nil {
		return db.Space{}, nil, err
	}
	if se != nil {
		return db.Space{}, se, nil
	}
	return space, nil, nil
}

// ListSpaces — GET /workspaces/{id}/spaces
func (h *WorkspaceHandlers) ListSpaces(ctx context.Context, request ListSpacesRequestObject) (ListSpacesResponseObject, error) {
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
	rows, err := h.spaces.List(ctx, ws, p.UserID)
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
func (h *WorkspaceHandlers) CreateSpace(ctx context.Context, request CreateSpaceRequestObject) (CreateSpaceResponseObject, error) {
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
	var defaultRole *string
	if request.Body.DefaultRole != nil {
		r := string(*request.Body.DefaultRole)
		defaultRole = &r
	}
	visibility := string(request.Body.Visibility)
	space, err := h.spaces.Create(ctx, ws, p.UserID, spaces.Fields{
		Name: &request.Body.Name, Description: request.Body.Description, Visibility: &visibility, DefaultRole: defaultRole,
	})
	var invalid *spaces.InvalidError
	switch {
	case errors.As(err, &invalid):
		return CreateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case errors.Is(err, spaces.ErrNameTaken):
		return CreateSpace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}

	ev.ResourceID = idString(space.ID)
	h.audited(ctx, ev.Allowed())
	return CreateSpace201JSONResponse(dbSpaceToPublic(space, "manager")), nil
}

// GetSpace — GET /workspaces/{id}/spaces/{space_id}
func (h *WorkspaceHandlers) GetSpace(ctx context.Context, request GetSpaceRequestObject) (GetSpaceResponseObject, error) {
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
	space, err := h.spaces.Get(ctx, ws, spaceID)
	if errors.Is(err, spaces.ErrNotFound) {
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
func (h *WorkspaceHandlers) UpdateSpace(ctx context.Context, request UpdateSpaceRequestObject) (UpdateSpaceResponseObject, error) {
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
	f := spaces.Fields{Name: request.Body.Name, Description: request.Body.Description}
	if request.Body.Visibility != nil {
		v := string(*request.Body.Visibility)
		f.Visibility = &v
	}
	if request.Body.DefaultRole != nil {
		r := string(*request.Body.DefaultRole)
		f.DefaultRole = &r
	}
	space, err := h.spaces.Update(ctx, ws, spaceID, f)
	var invalid *spaces.InvalidError
	switch {
	case errors.As(err, &invalid):
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case errors.Is(err, spaces.ErrNameTaken):
		return UpdateSpace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeConflict, err.Error())}, nil
	case err != nil:
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return UpdateSpace200JSONResponse(dbSpaceToPublic(space, "manager")), nil
}

// DeleteSpace — DELETE /workspaces/{id}/spaces/{space_id}
func (h *WorkspaceHandlers) DeleteSpace(ctx context.Context, request DeleteSpaceRequestObject) (DeleteSpaceResponseObject, error) {
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
	err = h.spaces.Delete(ctx, ws, spaceID, request.Params.Force != nil && *request.Params.Force)
	switch {
	case errors.Is(err, spaces.ErrDefaultSpace), errors.Is(err, spaces.ErrNotEmpty):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteSpace409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	case errors.Is(err, spaces.ErrNotFound):
		return DeleteSpace404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	case err != nil:
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return DeleteSpace204Response{}, nil
}

// ListSpaceMembers — GET /workspaces/{id}/spaces/{space_id}/members
func (h *WorkspaceHandlers) ListSpaceMembers(ctx context.Context, request ListSpaceMembersRequestObject) (ListSpaceMembersResponseObject, error) {
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
	rows, err := h.spaces.Members(ctx, spaceID)
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
func (h *WorkspaceHandlers) SetSpaceMember(ctx context.Context, request SetSpaceMemberRequestObject) (SetSpaceMemberResponseObject, error) {
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
	member, err := h.spaces.SetMember(ctx, spaceID, request.PrincipalType, principalID, string(request.Body.Role), p.UserID)
	var invalid *spaces.InvalidError
	switch {
	case errors.Is(err, spaces.ErrUserNotFound), errors.Is(err, spaces.ErrTeamNotFound):
		return SetSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound(err.Error())}, nil
	case errors.As(err, &invalid):
		return SetSpaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case err != nil:
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return SetSpaceMember200JSONResponse(dbSpaceMemberToPublic(member)), nil
}

// DeleteSpaceMember — DELETE /workspaces/{id}/spaces/{space_id}/members/{principal_type}/{principal_id}
func (h *WorkspaceHandlers) DeleteSpaceMember(ctx context.Context, request DeleteSpaceMemberRequestObject) (DeleteSpaceMemberResponseObject, error) {
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
	err = h.spaces.RemoveMember(ctx, spaceID, request.PrincipalType, principalID)
	if errors.Is(err, spaces.ErrMemberNotFound) {
		return DeleteSpaceMember404JSONResponse{NotFoundJSONResponse: metadataNotFound(err.Error())}, nil
	}
	if err != nil {
		return nil, err
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
