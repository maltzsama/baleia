package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListUsers(ctx context.Context, request ListUsersRequestObject) (ListUsersResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return ListUsers403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	users, err := h.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}

	data := make([]UserPublic, 0, len(users))
	for _, u := range users {
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

func (h *Handlers) InviteUser(ctx context.Context, request InviteUserRequestObject) (InviteUserResponseObject, error) {
	ev := audit.Event{Action: audit.UserInvite, ResourceType: "user"}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return InviteUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	_, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err == nil {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return InviteUser409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "email already exists"}}, nil
	}

	tempPassword := randomState()
	hash, err := auth.HashPasswordWithCost(tempPassword, h.cfg.PasswordCost())
	if err != nil {
		return nil, err
	}

	user, err := h.queries.CreateUser(ctx, db.CreateUserParams{
		Name:         string(request.Body.Email),
		Email:        string(request.Body.Email),
		Role:         string(request.Body.Role),
		AuthProvider: "local",
		PasswordHash: &hash,
	})
	if err != nil {
		slog.Error("CreateUser failed", "err", err)
		return nil, err
	}

	token := randomState()
	expires := time.Now().Add(72 * time.Hour)

	if err := h.queries.SetPasswordResetToken(ctx, db.SetPasswordResetTokenParams{
		Email:                  user.Email,
		PasswordResetToken:     &token,
		PasswordResetExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		slog.Error("SetPasswordResetToken failed", "err", err)
		return nil, err
	}

	inviteURL := h.cfg.PublicURL + "/accept-invite?token=" + token
	if err := sendResetEmail(h.cfg, user.Email, inviteURL); err != nil {
		slog.Warn("failed to send invite email", "err", err, "url", inviteURL)
	}

	ev = withID(ev, user.ID)
	ev.Changes = map[string]audit.Change{"role": {To: user.Role}}
	h.audited(ctx, ev.Allowed())
	return InviteUser201JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) UpdateUserRole(ctx context.Context, request UpdateUserRoleRequestObject) (UpdateUserRoleResponseObject, error) {
	ev := audit.Event{Action: audit.UserRoleChange, ResourceType: "user", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateUserRole403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	before, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateUserRole404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "user not found"}}, nil
	}

	if err := h.queries.UpdateUserRole(ctx, db.UpdateUserRoleParams{
		ID:   uid,
		Role: string(request.Body.Role),
	}); err != nil {
		return nil, err
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return nil, err
	}

	ev.Changes = map[string]audit.Change{"role": {From: before.Role, To: user.Role}}
	h.audited(ctx, ev.Allowed())
	return UpdateUserRole200JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) RemoveUser(ctx context.Context, request RemoveUserRequestObject) (RemoveUserResponseObject, error) {
	ev := audit.Event{Action: audit.UserDisable, ResourceType: "user", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return RemoveUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RemoveUser404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "user not found"}}, nil
	}

	if err := h.queries.DeleteUser(ctx, uid); err != nil {
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
		return ConflictJSONResponse{Code: "user_owns_workspace", Message: "the user owns a workspace; removing the user would delete its content"}, true
	}
	return ConflictJSONResponse{Code: "user_in_use", Message: "the user is still referenced (" + pg.TableName + ")"}, true
}
