package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/workspaces/teams"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *WorkspaceHandlers) ListTeams(ctx context.Context, request ListTeamsRequestObject) (ListTeamsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTeams401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	// A global admin sees every team; anyone else sees only the teams they belong to.
	list, err := h.teams.List(ctx, p.UserID, p.IsAdmin())
	if err != nil {
		return nil, err
	}

	result := make([]Team, 0, len(list))
	for _, t := range list {
		result = append(result, dbTeamToPublic(t))
	}
	return ListTeams200JSONResponse(result), nil
}

func (h *WorkspaceHandlers) CreateTeam(ctx context.Context, request CreateTeamRequestObject) (CreateTeamResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return CreateTeam403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	team, err := h.teams.Create(ctx, request.Body.Name, request.Body.Description, uid)
	if err != nil {
		return nil, err
	}

	return CreateTeam201JSONResponse(dbTeamToPublic(team)), nil
}

func (h *WorkspaceHandlers) GetTeam(ctx context.Context, request GetTeamRequestObject) (GetTeamResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetTeam401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	team, err := h.authz().Team(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return GetTeam404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "team not found"}}, nil
		}
		return nil, err
	}

	return GetTeam200JSONResponse(dbTeamToPublic(team)), nil
}

func (h *WorkspaceHandlers) DeleteTeam(ctx context.Context, request DeleteTeamRequestObject) (DeleteTeamResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return DeleteTeam403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	err := h.teams.Delete(ctx, uid)
	if errors.Is(err, teams.ErrNotFound) {
		return DeleteTeam404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}

	return DeleteTeam204Response{}, nil
}

func (h *WorkspaceHandlers) ListTeamMembers(ctx context.Context, request ListTeamMembersRequestObject) (ListTeamMembersResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTeamMembers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	uid := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.authz().Team(ctx, p, uid, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListTeamMembers404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "team not found"}}, nil
		}
		return nil, err
	}

	members, err := h.teams.Members(ctx, uid)
	if err != nil {
		return nil, err
	}

	result := make([]TeamMember, 0, len(members))
	for _, m := range members {
		result = append(result, TeamMember{
			Role:     TeamMemberRole(m.Role),
			JoinedAt: pgTimestampToTime(m.JoinedAt),
			User: UserPublic{
				Id:           openapi_types.UUID(m.UserID.Bytes),
				Name:         m.UserName,
				Email:        openapi_types.Email(m.UserEmail),
				Role:         UserPublicRole(m.UserRole),
				AuthProvider: UserPublicAuthProvider(m.UserAuthProvider),
				Status:       UserPublicStatus(m.UserStatus),
				CreatedAt:    pgTimestampToTime(m.UserCreatedAt),
			},
		})
	}
	return ListTeamMembers200JSONResponse(result), nil
}

func (h *WorkspaceHandlers) AddTeamMember(ctx context.Context, request AddTeamMemberRequestObject) (AddTeamMemberResponseObject, error) {
	ev := audit.Event{Action: audit.TeamMemberAdd, ResourceType: "team", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return AddTeamMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	var teamID pgtype.UUID
	teamID.Bytes = request.Id
	teamID.Valid = true

	var userID pgtype.UUID
	userID.Bytes = request.Body.UserId
	userID.Valid = true

	memberRole := string(request.Body.Role)

	member, err := h.teams.AddMember(ctx, teamID, userID, memberRole)
	switch {
	case errors.Is(err, teams.ErrNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return AddTeamMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case errors.Is(err, teams.ErrAlreadyMember):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return AddTeamMember409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	case errors.Is(err, teams.ErrUserNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return AddTeamMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	h.catalogCache.Clear()
	ev.Changes = map[string]audit.Change{"member": {To: idString(userID)}, "role": {To: memberRole}}
	h.audited(ctx, ev.Allowed())

	user, err := h.teams.User(ctx, userID)
	if err != nil {
		return nil, err
	}

	return AddTeamMember201JSONResponse{
		Role:     TeamMemberRole(member.Role),
		JoinedAt: pgTimestampToTime(member.JoinedAt),
		User:     dbUserToPublic(user),
	}, nil
}

// UpdateTeamMemberRole — PATCH /teams/{id}/members/{user_id} (#288).
func (h *WorkspaceHandlers) UpdateTeamMemberRole(ctx context.Context, request UpdateTeamMemberRoleRequestObject) (UpdateTeamMemberRoleResponseObject, error) {
	ev := audit.Event{Action: audit.TeamRoleChange, ResourceType: "team", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true}),
		Changes: map[string]audit.Change{"member": {To: idString(pgtype.UUID{Bytes: request.UserId, Valid: true})}}}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateTeamMemberRole403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}
	if request.Body == nil {
		return UpdateTeamMemberRole422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "request body is required")}, nil
	}

	teamID := pgtype.UUID{Bytes: request.Id, Valid: true}
	userID := pgtype.UUID{Bytes: request.UserId, Valid: true}
	memberRole := string(request.Body.Role)
	member, err := h.teams.SetRole(ctx, teamID, userID, memberRole)
	switch {
	case errors.Is(err, teams.ErrNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateTeamMemberRole404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case errors.Is(err, teams.ErrInvalidRole):
		return UpdateTeamMemberRole422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	case errors.Is(err, teams.ErrMemberNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateTeamMemberRole404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}

	h.catalogCache.Clear()
	ev.Changes["role"] = audit.Change{To: memberRole}
	h.audited(ctx, ev.Allowed())

	user, err := h.teams.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	return UpdateTeamMemberRole200JSONResponse(TeamMember{
		User:     dbUserToPublic(user),
		Role:     TeamMemberRole(member.Role),
		JoinedAt: pgTimestampToTime(member.JoinedAt),
	}), nil
}

func (h *WorkspaceHandlers) RemoveTeamMember(ctx context.Context, request RemoveTeamMemberRequestObject) (RemoveTeamMemberResponseObject, error) {
	ev := audit.Event{Action: audit.TeamMemberRemove, ResourceType: "team", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true}),
		Changes: map[string]audit.Change{"member": {From: idString(pgtype.UUID{Bytes: request.UserId, Valid: true})}}}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return RemoveTeamMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	var teamID pgtype.UUID
	teamID.Bytes = request.Id
	teamID.Valid = true

	var userID pgtype.UUID
	userID.Bytes = request.UserId
	userID.Valid = true

	err := h.teams.RemoveMember(ctx, teamID, userID)
	if errors.Is(err, teams.ErrMemberNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RemoveTeamMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return RemoveTeamMember204Response{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbTeamToPublic(t db.Team) Team {
	id := openapi_types.UUID(t.ID.Bytes)
	team := Team{
		Id:        id,
		Name:      t.Name,
		CreatedAt: pgTimestampToTime(t.CreatedAt),
	}
	if t.Description != nil {
		team.Description = t.Description
	}
	return team
}
