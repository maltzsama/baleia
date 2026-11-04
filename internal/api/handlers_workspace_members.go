package api

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func workspaceMemberToPublic(r db.ListWorkspaceMembersRow) WorkspaceMember {
	return WorkspaceMember{
		User:    dbUserToPublic(r.User),
		Role:    WorkspaceRole(r.Role),
		Source:  WorkspaceMemberSource(r.Source),
		RoleSet: r.RoleSet,
	}
}

// ListWorkspaceMembers — GET /workspaces/{id}/members (#653). Any member may
// see who else is in the workspace and with which role.
func (h *WorkspaceHandlers) ListWorkspaceMembers(ctx context.Context, request ListWorkspaceMembersRequestObject) (ListWorkspaceMembersResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListWorkspaceMembers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	if _, err := h.authz().Workspace(ctx, p, wsID, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) || errors.Is(err, authz.ErrForbidden) {
			return ListWorkspaceMembers404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	rows, err := h.queries.ListWorkspaceMembers(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make(ListWorkspaceMembers200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, workspaceMemberToPublic(r))
	}
	return out, nil
}

// memberOutcome is the result of setting a member's role.
type memberOutcome int

const (
	memberSet memberOutcome = iota
	memberWorkspaceNotFound
	memberForbidden
	memberInvalidRole
	memberUserNotFound
	memberOwnerFixed
	memberNotInTeam
)

// setMember applies the rules of PUT and POST /workspaces/{id}/members
// (#653): only a workspace admin changes members; a shared workspace's
// members come from its team; a personal workspace's owner stays its admin.
// user nil means the e-mail matched no one.
func (h *core) setMember(ctx context.Context, wsID pgtype.UUID, user *db.User, role string) (db.ListWorkspaceMembersRow, memberOutcome, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return db.ListWorkspaceMembersRow{}, memberForbidden, nil
	}
	ev := audit.Event{Action: audit.WorkspaceMemberSet, ResourceType: "workspace", ResourceID: idString(wsID), WorkspaceID: idString(wsID),
		Changes: map[string]audit.Change{"role": {To: role}}}
	ctx = audit.WithAction(ctx, ev.Action)
	ws, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return db.ListWorkspaceMembersRow{}, memberWorkspaceNotFound, nil
	case errors.Is(err, authz.ErrForbidden):
		return db.ListWorkspaceMembersRow{}, memberForbidden, nil
	case err != nil:
		return db.ListWorkspaceMembersRow{}, 0, err
	}
	if authz.ParseWorkspaceRole(role) == authz.WorkspaceNone {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return db.ListWorkspaceMembersRow{}, memberInvalidRole, nil
	}
	if user == nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return db.ListWorkspaceMembersRow{}, memberUserNotFound, nil
	}
	ev.Changes["member"] = audit.Change{To: idString(user.ID)}
	if ws.Type == "personal" && ws.OwnerUserID == user.ID {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return db.ListWorkspaceMembersRow{}, memberOwnerFixed, nil
	}
	// The write itself checks a shared workspace's team membership, so a
	// concurrent removal from the team cannot leave a role that has no effect.
	n, err := h.queries.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: wsID, UserID: user.ID, Role: role, GrantedBy: p.UserID, Source: "manual"})
	if err != nil {
		return db.ListWorkspaceMembersRow{}, 0, err
	}
	if n == 0 {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return db.ListWorkspaceMembersRow{}, memberNotInTeam, nil
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	// Built from what was written: the change is committed and audited, so
	// a failed read-back must not turn it into an error.
	source := "team"
	if ws.Type == "personal" {
		source = "shared"
	}
	return db.ListWorkspaceMembersRow{User: *user, Role: role, Source: source, RoleSet: true}, memberSet, nil
}

// lookupUser returns nil when no user matches.
func lookupUser(u db.User, err error) (*db.User, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SetWorkspaceMember — PUT /workspaces/{id}/members/{user_id} (#653).
func (h *WorkspaceHandlers) SetWorkspaceMember(ctx context.Context, request SetWorkspaceMemberRequestObject) (SetWorkspaceMemberResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return SetWorkspaceMember401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	role := ""
	if request.Body != nil {
		role = string(request.Body.Role)
	}
	user, err := lookupUser(h.queries.GetUserByID(ctx, pgtype.UUID{Bytes: request.UserId, Valid: true}))
	if err != nil {
		return nil, err
	}
	row, outcome, err := h.setMember(ctx, workspaceIDFromRequest(request.Id), user, role)
	if err != nil {
		return nil, err
	}
	switch outcome {
	case memberWorkspaceNotFound:
		return SetWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	case memberForbidden:
		return SetWorkspaceMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace admin may change members"}}, nil
	case memberInvalidRole:
		return SetWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "role must be admin, editor or viewer")}, nil
	case memberUserNotFound:
		return SetWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
	case memberOwnerFixed:
		return SetWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeWorkspaceOwnerRoleFixed, "the owner of a personal workspace is always its admin")}, nil
	case memberNotInTeam:
		return SetWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeWorkspaceMemberNotInTeam, "the user is not in the workspace's team")}, nil
	}
	return SetWorkspaceMember200JSONResponse(workspaceMemberToPublic(row)), nil
}

// AddWorkspaceMember — POST /workspaces/{id}/members (#653): the same rules,
// addressing the user by e-mail.
func (h *WorkspaceHandlers) AddWorkspaceMember(ctx context.Context, request AddWorkspaceMemberRequestObject) (AddWorkspaceMemberResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return AddWorkspaceMember401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return AddWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "email and role are required")}, nil
	}
	user, err := lookupUser(h.queries.GetUserByEmail(ctx, strings.TrimSpace(string(request.Body.Email))))
	if err != nil {
		return nil, err
	}
	row, outcome, err := h.setMember(ctx, workspaceIDFromRequest(request.Id), user, string(request.Body.Role))
	if err != nil {
		return nil, err
	}
	switch outcome {
	case memberWorkspaceNotFound:
		return AddWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	case memberForbidden:
		return AddWorkspaceMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace admin may change members"}}, nil
	case memberInvalidRole:
		return AddWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "role must be admin, editor or viewer")}, nil
	case memberUserNotFound:
		return AddWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
	case memberOwnerFixed:
		return AddWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeWorkspaceOwnerRoleFixed, "the owner of a personal workspace is always its admin")}, nil
	case memberNotInTeam:
		return AddWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeWorkspaceMemberNotInTeam, "the user is not in the workspace's team")}, nil
	}
	return AddWorkspaceMember200JSONResponse(workspaceMemberToPublic(row)), nil
}

// RemoveWorkspaceMember — DELETE /workspaces/{id}/members/{user_id} (#653):
// stops sharing a personal workspace, or resets a team member's role.
func (h *WorkspaceHandlers) RemoveWorkspaceMember(ctx context.Context, request RemoveWorkspaceMemberRequestObject) (RemoveWorkspaceMemberResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return RemoveWorkspaceMember401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	userID := pgtype.UUID{Bytes: request.UserId, Valid: true}
	ev := audit.Event{Action: audit.WorkspaceMemberRemove, ResourceType: "workspace", ResourceID: idString(wsID), WorkspaceID: idString(wsID),
		Changes: map[string]audit.Change{"member": {From: idString(userID)}}}
	ctx = audit.WithAction(ctx, ev.Action)
	ws, err := h.authz().Workspace(ctx, p, wsID, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return RemoveWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	case errors.Is(err, authz.ErrForbidden):
		return RemoveWorkspaceMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace admin may change members"}}, nil
	case err != nil:
		return nil, err
	}
	if ws.Type == "personal" && ws.OwnerUserID == userID {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return RemoveWorkspaceMember422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeWorkspaceOwnerRoleFixed, "the owner of a personal workspace is always its admin")}, nil
	}
	n, err := h.queries.DeleteWorkspaceMember(ctx, db.DeleteWorkspaceMemberParams{WorkspaceID: wsID, UserID: userID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RemoveWorkspaceMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "no role was set for this user"}}, nil
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return RemoveWorkspaceMember204Response{}, nil
}
