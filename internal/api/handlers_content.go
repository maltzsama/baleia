package api

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/workbench/folders"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// decorateSpace adds the counts and allowed actions the content UI needs (#113).
func (h *core) decorateSpace(ctx context.Context, sp *Space, role string) {
	counts, err := h.queries.CountSpaceContentKinds(ctx, pgtype.UUID{Bytes: [16]byte(sp.Id), Valid: true})
	if err == nil {
		saved, metrics, models, folders := int(counts.SavedQueries), int(counts.SemanticMetrics), int(counts.SemanticModels), int(counts.Folders)
		sp.Counts = &SpaceCounts{SavedQueries: &saved, SemanticMetrics: &metrics, SemanticModels: &models, Folders: &folders}
	}
	acts := spaceActions(role, sp.IsDefault)
	sp.AllowedActions = &acts
}

func spaceActions(role string, isDefault bool) []string {
	acts := []string{"view"}
	switch role {
	case "editor":
		acts = append(acts, "edit_content")
	case "manager":
		acts = append(acts, "edit_content", "manage")
		if !isDefault {
			acts = append(acts, "delete")
		}
	}
	return acts
}

// SearchContent — GET /workspaces/{id}/content/search (#113).
func (h *SemanticHandlers) SearchContent(ctx context.Context, request SearchContentRequestObject) (SearchContentResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SearchContent401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if _, err := h.authz().Workspace(ctx, p, ws, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return SearchContent404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
		}
		return nil, err
	}
	sq := search.Query{Text: request.Params.Q}
	if request.Params.Limit != nil {
		sq.Limit = *request.Params.Limit
	}
	if request.Params.Kind != nil {
		sq.Kind = *request.Params.Kind
	}
	if request.Params.SpaceId != nil {
		sq.SpaceID = pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}
	hits, err := h.search.Search(ctx, ws, p.UserID, sq)
	if err != nil {
		return nil, err
	}
	items := make([]ContentSearchItem, 0, len(hits))
	for _, hit := range hits {
		path := make([]ContentSearchFolderPath, 0, len(hit.FolderPath))
		for _, f := range hit.FolderPath {
			path = append(path, ContentSearchFolderPath{Id: openapi_types.UUID(f.ID.Bytes), Name: f.Name})
		}
		items = append(items, ContentSearchItem{
			Kind:       hit.Kind,
			Id:         openapi_types.UUID(hit.ID.Bytes),
			Slug:       hit.Slug,
			Title:      hit.Title,
			Space:      ContentSearchSpace{Id: openapi_types.UUID(hit.SpaceID.Bytes), Name: hit.SpaceName},
			FolderPath: path,
			UpdatedAt:  hit.UpdatedAt,
			Certified:  hit.Certified,
		})
	}
	return SearchContent200JSONResponse(items), nil
}

// ListWorkspacePrincipals — GET /workspaces/{id}/principals (#113). Any
// workspace member may search the people and teams they can add to a space.
func (h *SemanticHandlers) ListWorkspacePrincipals(ctx context.Context, request ListWorkspacePrincipalsRequestObject) (ListWorkspacePrincipalsResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListWorkspacePrincipals401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if _, err := h.authz().Workspace(ctx, p, ws, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListWorkspacePrincipals404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
		}
		return nil, err
	}
	limit := 20
	if request.Params.Limit != nil && *request.Params.Limit >= 1 && *request.Params.Limit <= 50 {
		limit = *request.Params.Limit
	}
	q := ""
	if request.Params.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*request.Params.Q))
	}
	kind := ""
	if request.Params.Type != nil {
		kind = *request.Params.Type
	}
	out := make([]WorkspacePrincipal, 0, limit)
	if kind == "" || kind == "user" {
		users, err := h.queries.ListWorkspacePrincipalUsers(ctx, db.ListWorkspacePrincipalUsersParams{
			WorkspaceID: ws, Lim: int32(limit),
		})
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if q != "" && !strings.Contains(strings.ToLower(user.Name), q) && !strings.Contains(strings.ToLower(user.Email), q) {
				continue
			}
			email := user.Email
			out = append(out, WorkspacePrincipal{
				Type: "user", Id: openapi_types.UUID(user.ID.Bytes), Name: user.Name, Email: &email,
			})
			if len(out) >= limit {
				break
			}
		}
	}
	if (kind == "" || kind == "team") && len(out) < limit {
		teams, err := h.queries.ListTeamsByUser(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			if q != "" && !strings.Contains(strings.ToLower(team.Name), q) {
				continue
			}
			members, err := h.queries.ListTeamMembers(ctx, team.ID)
			if err != nil {
				continue
			}
			count := len(members)
			out = append(out, WorkspacePrincipal{
				Type: "team", Id: openapi_types.UUID(team.ID.Bytes), Name: team.Name, MemberCount: &count,
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return ListWorkspacePrincipals200JSONResponse(out), nil
}

// UpdateFolder — PATCH /workspaces/{id}/folders/{folder_id} (#113). Renames a
// folder or moves its subtree within or across spaces.
func (h *QueryHandlers) UpdateFolder(ctx context.Context, request UpdateFolderRequestObject) (UpdateFolderResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if _, err := h.authz().Workspace(ctx, p, ws, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return UpdateFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "folder not found"}}, nil
		}
		return nil, err
	}
	folderID := pgtype.UUID{Bytes: request.FolderId, Valid: true}
	folder, err := h.folders.Get(ctx, ws, folderID)
	if errors.Is(err, folders.ErrNotFound) {
		return UpdateFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "folder not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	if _, se, err := h.spaceForWrite(ctx, ws, uuidPtr(folder.SpaceID), authz.SpaceEditor); err != nil {
		return nil, err
	} else if se != nil {
		return UpdateFolder403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
	}

	destSpace := folder.SpaceID
	if request.Body.SpaceId != nil {
		destSpace = pgtype.UUID{Bytes: [16]byte(*request.Body.SpaceId), Valid: true}
		if destSpace != folder.SpaceID {
			if _, se, err := h.spaceForWrite(ctx, ws, uuidPtr(destSpace), authz.SpaceEditor); err != nil {
				return nil, err
			} else if se != nil {
				return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeSpaceNotFound, "destination space not found")}, nil
			}
		}
	}

	edit := folders.Edit{Name: request.Body.Name, Slug: request.Body.Slug, SpaceID: destSpace}
	if request.Body.ParentId != nil {
		edit.Parent = &pgtype.UUID{Bytes: [16]byte(*request.Body.ParentId), Valid: true}
	}
	updated, err := h.folders.Update(ctx, folder, edit)
	var cycle *folders.CycleError
	switch {
	case errors.Is(err, folders.ErrEmptyName):
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	case errors.Is(err, folders.ErrInvalidSlug):
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "slug must match ^[a-z][a-z0-9_]{0,62}$")}, nil
	case errors.Is(err, folders.ErrParentNotFound):
		return UpdateFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "parent folder not found"}}, nil
	case errors.Is(err, folders.ErrSlugConflict):
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeSlugConflict, err.Error())}, nil
	case errors.As(err, &cycle):
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeFolderCycle, cycle.Msg)}, nil
	case err != nil:
		return nil, err
	}
	return UpdateFolder200JSONResponse(dbFolderToPublic(updated)), nil
}

func uuidPtr(id pgtype.UUID) *openapi_types.UUID {
	if !id.Valid {
		return nil
	}
	out := openapi_types.UUID(id.Bytes)
	return &out
}
