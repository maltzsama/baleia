package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
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
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return InviteUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	_, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err == nil {
		return InviteUser409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "email already exists"}}, nil
	}

	tempPassword := randomState()
	hash, err := auth.HashPassword(tempPassword)
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

	return InviteUser201JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) UpdateUserRole(ctx context.Context, request UpdateUserRoleRequestObject) (UpdateUserRoleResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return UpdateUserRole403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
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

	return UpdateUserRole200JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) RemoveUser(ctx context.Context, request RemoveUserRequestObject) (RemoveUserResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return RemoveUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return RemoveUser404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "user not found"}}, nil
	}

	if err := h.queries.DeleteUser(ctx, uid); err != nil {
		return nil, err
	}

	return RemoveUser204Response{}, nil
}