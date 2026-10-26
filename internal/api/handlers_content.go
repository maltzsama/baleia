package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/workbench/folders"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// decorateSpace adds the counts and allowed actions the content UI needs (#113).
func (h *core) decorateSpace(ctx context.Context, sp *Space, role string) {
	counts, err := h.spaces.ContentKinds(ctx, pgtype.UUID{Bytes: [16]byte(sp.Id), Valid: true})
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
	f := wsaccess.PrincipalFilter{Limit: limit}
	if request.Params.Q != nil {
		f.Q = *request.Params.Q
	}
	if request.Params.Type != nil {
		f.Kind = *request.Params.Type
	}
	principals, err := h.access.Principals(ctx, ws, p.UserID, f)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspacePrincipal, 0, len(principals))
	for _, pr := range principals {
		item := WorkspacePrincipal{Type: pr.Kind, Id: openapi_types.UUID(pr.ID.Bytes), Name: pr.Name}
		if pr.Kind == "user" {
			email := pr.Email
			item.Email = &email
		} else {
			count := pr.Members
			item.MemberCount = &count
		}
		out = append(out, item)
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
