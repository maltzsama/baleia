package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/identity/users"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *AuthHandlers) ListUsers(ctx context.Context, request ListUsersRequestObject) (ListUsersResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return ListUsers403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	all, err := h.users.List(ctx)
	if err != nil {
		return nil, err
	}

	data := make([]UserPublic, 0, len(all))
	for _, u := range all {
		data = append(data, dbUserToPublic(u))
	}

	page := 1
	if request.Params.Page != nil {
		page = *request.Params.Page
	}
	perPage := 20
	if request.Params.PerPage != nil {
		perPage = *request.Params.PerPage
	}
	total := len(data)
	totalPages := total / perPage
	if total%perPage != 0 {
		totalPages++
	}

	return ListUsers200JSONResponse{
		Data: &data,
		Pagination: &Pagination{
			Page:       page,
			PerPage:    perPage,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

func (h *AuthHandlers) InviteUser(ctx context.Context, request InviteUserRequestObject) (InviteUserResponseObject, error) {
	ev := audit.Event{Action: audit.UserInvite, ResourceType: "user"}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return InviteUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	user, err := h.users.Invite(ctx, string(request.Body.Email), string(request.Body.Role))
	if errors.Is(err, users.ErrEmailTaken) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return InviteUser409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "email already exists"}}, nil
	}
	if err != nil {
		return nil, err
	}

	ev = withID(ev, user.ID)
	ev.Changes = map[string]audit.Change{"role": {To: user.Role}}
	h.audited(ctx, ev.Allowed())
	return InviteUser201JSONResponse(dbUserToPublic(user)), nil
}

func (h *AuthHandlers) UpdateUserRole(ctx context.Context, request UpdateUserRoleRequestObject) (UpdateUserRoleResponseObject, error) {
	ev := audit.Event{Action: audit.UserRoleChange, ResourceType: "user", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateUserRole403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	// A role change takes effect on the next login: the old sessions end
	// (#51). Access tokens carry the old role until they expire.
	before, user, err := h.users.ChangeRole(ctx, uid, string(request.Body.Role))
	if errors.Is(err, users.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateUserRole404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	ev.Changes = map[string]audit.Change{"role": {From: before.Role, To: user.Role}}
	h.audited(ctx, ev.Allowed())
	return UpdateUserRole200JSONResponse(dbUserToPublic(user)), nil
}

func (h *AuthHandlers) RemoveUser(ctx context.Context, request RemoveUserRequestObject) (RemoveUserResponseObject, error) {
	ev := audit.Event{Action: audit.UserDisable, ResourceType: "user", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return RemoveUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	if err := h.users.Remove(ctx, uid); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			h.audited(ctx, ev.Failed(audit.ReasonNotFound))
			return RemoveUser404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
		}
		if c, ok := removeUserConflict(err); ok {
			h.audited(ctx, ev.Failed(audit.ReasonConflict))
			return RemoveUser409JSONResponse{ConflictJSONResponse: c}, nil
		}
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return RemoveUser204Response{}, nil
}

// removeUserConflict maps a delete the database refused because the user is
// still referenced: a workspace the user owns (every user who has logged in
// owns a personal one) keeps its content, so the removal is refused.
func removeUserConflict(err error) (ConflictJSONResponse, bool) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || !isFKViolation(pg) {
		return ConflictJSONResponse{}, false
	}
	if pg.ConstraintName == "workspaces_owner_user_id_fkey" {
		return ConflictJSONResponse{Code: ErrorCodeUserOwnsWorkspace, Message: "the user owns a workspace; removing the user would delete its content"}, true
	}
	return ConflictJSONResponse{Code: ErrorCodeUserInUse, Message: "the user is still referenced (" + pg.TableName + ")"}, true
}
