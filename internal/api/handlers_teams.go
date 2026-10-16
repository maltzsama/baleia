package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListTeams(ctx context.Context, request ListTeamsRequestObject) (ListTeamsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTeams401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	// A global admin sees every team; anyone else sees only the teams they belong to.
	var teams []db.Team
	if p.IsAdmin() {
		teams, err = h.queries.ListTeams(ctx)
	} else {
		teams, err = h.queries.ListTeamsByUser(ctx, p.UserID)
	}
	if err != nil {
		return nil, err
	}

	result := make([]Team, 0, len(teams))
	for _, t := range teams {
		result = append(result, dbTeamToPublic(t))
	}
	return ListTeams200JSONResponse(result), nil
}

func (h *Handlers) CreateTeam(ctx context.Context, request CreateTeamRequestObject) (CreateTeamResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return CreateTeam403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	team, err := h.queries.CreateTeam(ctx, db.CreateTeamParams{
		Name:        request.Body.Name,
		Description: request.Body.Description,
		CreatedBy:   uid,
	})
	if err != nil {
		return nil, err
	}

	return CreateTeam201JSONResponse(dbTeamToPublic(team)), nil
}

func (h *Handlers) GetTeam(ctx context.Context, request GetTeamRequestObject) (GetTeamResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetTeam401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	team, err := h.authz().Team(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return GetTeam404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "team not found"}}, nil
		}
		return nil, err
	}

	return GetTeam200JSONResponse(dbTeamToPublic(team)), nil
}

func (h *Handlers) DeleteTeam(ctx context.Context, request DeleteTeamRequestObject) (DeleteTeamResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return DeleteTeam403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetTeamByID(ctx, uid)
	if err != nil {
		return DeleteTeam404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "team not found"}}, nil
	}

	if err := h.queries.DeleteTeam(ctx, uid); err != nil {
		return nil, err
	}

	return DeleteTeam204Response{}, nil
}

func (h *Handlers) ListTeamMembers(ctx context.Context, request ListTeamMembersRequestObject) (ListTeamMembersResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTeamMembers401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	uid := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.authz().Team(ctx, p, uid, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListTeamMembers404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "team not found"}}, nil
		}
		return nil, err
	}

	members, err := h.queries.ListTeamMembers(ctx, uid)
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
				CreatedAt:    pgTimestampToTime(m.UserCreatedAt),
			},
		})
	}
	return ListTeamMembers200JSONResponse(result), nil
}

func (h *Handlers) AddTeamMember(ctx context.Context, request AddTeamMemberRequestObject) (AddTeamMemberResponseObject, error) {
	ev := audit.Event{Action: audit.TeamMemberAdd, ResourceType: "team", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return AddTeamMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var teamID pgtype.UUID
	teamID.Bytes = request.Id
	teamID.Valid = true

	_, err := h.queries.GetTeamByID(ctx, teamID)
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return AddTeamMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "team not found"}}, nil
	}

	var userID pgtype.UUID
	userID.Bytes = request.Body.UserId
	userID.Valid = true

	memberRole := string(request.Body.Role)

	member, err := h.queries.AddTeamMember(ctx, db.AddTeamMemberParams{
		TeamID: teamID,
		UserID: userID,
		Role:   memberRole,
	})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return AddTeamMember409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "user already in team"}}, nil
	}
	// Team membership decides who is a member of the team's workspaces.
	h.catalogCache.Clear()
	ev.Changes = map[string]audit.Change{"member": {To: idString(userID)}, "role": {To: memberRole}}
	h.audited(ctx, ev.Allowed())

	user, err := h.queries.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return AddTeamMember201JSONResponse{
		Role:     TeamMemberRole(member.Role),
		JoinedAt: pgTimestampToTime(member.JoinedAt),
		User:     dbUserToPublic(user),
	}, nil
}

func (h *Handlers) RemoveTeamMember(ctx context.Context, request RemoveTeamMemberRequestObject) (RemoveTeamMemberResponseObject, error) {
	ev := audit.Event{Action: audit.TeamMemberRemove, ResourceType: "team", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true}),
		Changes: map[string]audit.Change{"member": {From: idString(pgtype.UUID{Bytes: request.UserId, Valid: true})}}}
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return RemoveTeamMember403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var teamID pgtype.UUID
	teamID.Bytes = request.Id
	teamID.Valid = true

	var userID pgtype.UUID
	userID.Bytes = request.UserId
	userID.Valid = true

	if err := h.queries.RemoveTeamMember(ctx, db.RemoveTeamMemberParams{
		TeamID: teamID,
		UserID: userID,
	}); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RemoveTeamMember404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "member not found"}}, nil
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