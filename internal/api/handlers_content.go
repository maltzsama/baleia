package api

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var folderSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// decorateSpace adds the counts and allowed actions the content UI needs (#113).
func (h *Handlers) decorateSpace(ctx context.Context, sp *Space, role string) {
	counts, err := h.queries.CountSpaceContentKinds(ctx, pgtype.UUID{Bytes: [16]byte(sp.Id), Valid: true})
	if err == nil {
		saved, metrics, folders := int(counts.SavedQueries), int(counts.SemanticMetrics), int(counts.Folders)
		sp.Counts = &SpaceCounts{SavedQueries: &saved, SemanticMetrics: &metrics, Folders: &folders}
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
func (h *Handlers) SearchContent(ctx context.Context, request SearchContentRequestObject) (SearchContentResponseObject, error) {
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
	q := strings.TrimSpace(request.Params.Q)
	if len([]rune(q)) < 2 {
		return SearchContent200JSONResponse([]ContentSearchItem{}), nil
	}
	limit := 20
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	kind := ""
	if request.Params.Kind != nil {
		kind = *request.Params.Kind
	}
	var spaceID pgtype.UUID
	if request.Params.SpaceId != nil {
		spaceID = pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}

	spaces, err := h.queries.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	spaceNames := make(map[[16]byte]string, len(spaces))
	for _, s := range spaces {
		spaceNames[s.ID.Bytes] = s.Name
	}

	folders, err := h.queries.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
		WorkspaceID: ws, UserID: p.UserID, SpaceID: spaceID,
	})
	if err != nil {
		return nil, err
	}
	folderByID := make(map[[16]byte]db.QueryFolder, len(folders))
	children := make(map[[16]byte][][16]byte, len(folders))
	for _, f := range folders {
		folderByID[f.ID.Bytes] = f
		if f.ParentID.Valid {
			children[f.ParentID.Bytes] = append(children[f.ParentID.Bytes], f.ID.Bytes)
		}
	}
	pathTo := func(id pgtype.UUID) []ContentSearchFolderPath {
		out := []ContentSearchFolderPath{}
		seen := map[[16]byte]bool{}
		for id.Valid && !seen[id.Bytes] {
			seen[id.Bytes] = true
			f, ok := folderByID[id.Bytes]
			if !ok {
				break
			}
			out = append([]ContentSearchFolderPath{{Id: openapi_types.UUID(f.ID.Bytes), Name: f.Name}}, out...)
			id = f.ParentID
		}
		return out
	}

	items := make([]ContentSearchItem, 0, limit)
	lower := strings.ToLower(q)
	if kind == "" || kind == "folder" {
		for _, f := range folders {
			if !strings.Contains(strings.ToLower(f.Name), lower) && !strings.Contains(strings.ToLower(f.Slug), lower) {
				continue
			}
			items = append(items, ContentSearchItem{
				Kind:       "folder",
				Id:         openapi_types.UUID(f.ID.Bytes),
				Slug:       f.Slug,
				Title:      f.Name,
				Space:      ContentSearchSpace{Id: openapi_types.UUID(f.SpaceID.Bytes), Name: spaceNames[f.SpaceID.Bytes]},
				FolderPath: pathTo(f.ParentID),
				UpdatedAt:  pgTimestampToTime(f.CreatedAt),
			})
		}
	}
	if kind == "" || kind == "saved_query" {
		queries, err := h.queries.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
			WorkspaceID: ws, UserID: p.UserID, SpaceID: spaceID, Q: &q,
		})
		if err != nil {
			return nil, err
		}
		for _, query := range queries {
			items = append(items, ContentSearchItem{
				Kind:       "saved_query",
				Id:         openapi_types.UUID(query.ID.Bytes),
				Slug:       query.Slug,
				Title:      query.Name,
				Space:      ContentSearchSpace{Id: openapi_types.UUID(query.SpaceID.Bytes), Name: spaceNames[query.SpaceID.Bytes]},
				FolderPath: pathTo(query.FolderID),
				UpdatedAt:  pgTimestampToTime(query.UpdatedAt),
			})
		}
	}
	if kind == "" || kind == "semantic_metric" {
		metrics, err := h.queries.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{
			WorkspaceID: ws, UserID: p.UserID, SpaceID: spaceID,
		})
		if err != nil {
			return nil, err
		}
		for _, metric := range metrics {
			if !strings.Contains(strings.ToLower(metric.Label), lower) && !strings.Contains(strings.ToLower(metric.Slug), lower) {
				continue
			}
			certified := metric.Certified
			items = append(items, ContentSearchItem{
				Kind:       "semantic_metric",
				Id:         openapi_types.UUID(metric.ID.Bytes),
				Slug:       metric.Slug,
				Title:      metric.Label,
				Space:      ContentSearchSpace{Id: openapi_types.UUID(metric.SpaceID.Bytes), Name: spaceNames[metric.SpaceID.Bytes]},
				FolderPath: []ContentSearchFolderPath{},
				UpdatedAt:  pgTimestampToTime(metric.UpdatedAt),
				Certified:  &certified,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title) })
	if len(items) > limit {
		items = items[:limit]
	}
	return SearchContent200JSONResponse(items), nil
}

// ListWorkspacePrincipals — GET /workspaces/{id}/principals (#113). Any
// workspace member may search the people and teams they can add to a space.
func (h *Handlers) ListWorkspacePrincipals(ctx context.Context, request ListWorkspacePrincipalsRequestObject) (ListWorkspacePrincipalsResponseObject, error) {
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
func (h *Handlers) UpdateFolder(ctx context.Context, request UpdateFolderRequestObject) (UpdateFolderResponseObject, error) {
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
	folder, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: folderID, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
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

	name := folder.Name
	if request.Body.Name != nil {
		name = strings.TrimSpace(*request.Body.Name)
	}
	if name == "" {
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "name cannot be empty")}, nil
	}
	slug := folder.Slug
	if request.Body.Slug != nil {
		slug = *request.Body.Slug
	}
	if !folderSlugRe.MatchString(slug) {
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "slug must match "+folderSlugRe.String())}, nil
	}
	exists, err := h.queries.FolderSlugExists(ctx, db.FolderSlugExistsParams{SpaceID: destSpace, Slug: slug})
	if err != nil {
		return nil, err
	}
	if exists && destSpace != folder.SpaceID {
		return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeSlugConflict, "another folder in the destination space uses this slug")}, nil
	}

	parent := folder.ParentID
	if request.Body.ParentId != nil {
		parent = pgtype.UUID{Bytes: [16]byte(*request.Body.ParentId), Valid: true}
	}
	if destSpace != folder.SpaceID && request.Body.ParentId == nil {
		parent = pgtype.UUID{}
	}
	if parent.Valid {
		if parent == folderID {
			return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeFolderCycle, "a folder cannot be its own parent")}, nil
		}
		cur := parent
		for depth := 0; depth < 128 && cur.Valid; depth++ {
			if cur == folderID {
				return UpdateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeFolderCycle, "cannot move a folder under its own descendant")}, nil
			}
			next, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: cur, WorkspaceID: ws})
			if err != nil {
				break
			}
			cur = next.ParentID
		}
	}

	updated, err := h.queries.UpdateQueryFolder(ctx, db.UpdateQueryFolderParams{
		Name: name, Slug: &slug, ParentID: parent, SpaceID: destSpace, ID: folderID, WorkspaceID: ws,
	})
	if err != nil {
		return nil, err
	}
	if destSpace != folder.SpaceID {
		if err := h.queries.MoveQueryFolderSubtree(ctx, db.MoveQueryFolderSubtreeParams{ID: folderID, WorkspaceID: ws, SpaceID: destSpace}); err != nil {
			return nil, err
		}
		if err := h.queries.MoveSavedQueriesInFolderSubtree(ctx, db.MoveSavedQueriesInFolderSubtreeParams{ID: folderID, WorkspaceID: ws, SpaceID: destSpace}); err != nil {
			return nil, err
		}
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
