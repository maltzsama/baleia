package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/trinoidentity"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// ── Folders ───────────────────────────────────────────────────────────────────

func (h *Handlers) ListFolders(ctx context.Context, request ListFoldersRequestObject) (ListFoldersResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListFolders401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var parentID pgtype.UUID
	if request.Params.ParentId != nil {
		parentID.Bytes = *request.Params.ParentId
		parentID.Valid = true
	}

	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var spaceID pgtype.UUID
	if request.Params.SpaceId != nil {
		spaceID = pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}
	var folders []db.QueryFolder
	if request.Params.ParentId != nil {
		folders, err = h.queries.ListVisibleQueryFolders(ctx, db.ListVisibleQueryFoldersParams{
			WorkspaceID: workspaceIDFromRequest(request.Id),
			UserID:      userID,
			SpaceID:     spaceID,
			ParentID:    parentID,
		})
	} else {
		folders, err = h.queries.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
			WorkspaceID: workspaceIDFromRequest(request.Id),
			UserID:      userID,
			SpaceID:     spaceID,
		})
	}
	if err != nil {
		return nil, err
	}

	result := make([]QueryFolder, 0, len(folders))
	for _, f := range folders {
		result = append(result, dbFolderToPublic(f))
	}
	return ListFolders200JSONResponse(result), nil
}

func (h *Handlers) CreateFolder(ctx context.Context, request CreateFolderRequestObject) (CreateFolderResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CreateFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	ws := workspaceIDFromRequest(request.Id)
	space, se, err := h.spaceForWrite(ctx, ws, request.Body.SpaceId, authz.SpaceEditor)
	if err != nil {
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return CreateFolder403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return CreateFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "space not found"}}, nil
	}

	var parentID pgtype.UUID
	if request.Body.ParentId != nil {
		parentID = pgtype.UUID{Bytes: *request.Body.ParentId, Valid: true}
		parent, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: parentID, WorkspaceID: ws})
		if err != nil || parent.SpaceID != space.ID {
			return CreateFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "parent folder not found"}}, nil
		}
	}

	slug := ""
	if request.Body.Slug != nil {
		slug = *request.Body.Slug
	}
	if slug == "" {
		slug, err = uniqueSlug(ctx, request.Body.Name, func(ctx context.Context, s string) (bool, error) {
			return h.queries.FolderSlugExists(ctx, db.FolderSlugExistsParams{SpaceID: space.ID, Slug: s})
		})
		if err != nil {
			return nil, err
		}
	} else if !slugRe.MatchString(slug) {
		return CreateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "slug must match ^[a-z][a-z0-9_]{0,62}$")}, nil
	}

	folder, err := h.queries.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: ws,
		SpaceID:     space.ID,
		ParentID:    parentID,
		Name:        request.Body.Name,
		Slug:        slug,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CreateFolder422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("conflict", "a folder with this slug already exists in the space")}, nil
		}
		return nil, err
	}

	return CreateFolder201JSONResponse(dbFolderToPublic(folder)), nil
}

func (h *Handlers) DeleteFolder(ctx context.Context, request DeleteFolderRequestObject) (DeleteFolderResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return DeleteFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	ws := workspaceIDFromRequest(request.Id)
	folderID := pgtype.UUID{Bytes: request.FolderId, Valid: true}
	folder, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: folderID, WorkspaceID: ws})
	if err != nil {
		return DeleteFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
	}
	_, se, err := h.requireSpace(ctx, ws, folder.SpaceID, authz.SpaceEditor)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteFolder401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return DeleteFolder403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return DeleteFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
	}

	deleted, err := h.queries.DeleteQueryFolder(ctx, db.DeleteQueryFolderParams{WorkspaceID: ws, ID: folderID})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteFolder404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
	}

	return DeleteFolder204Response{}, nil
}

// ── Saved Queries ─────────────────────────────────────────────────────────────

func (h *Handlers) ListSavedQueries(ctx context.Context, request ListSavedQueriesRequestObject) (ListSavedQueriesResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListSavedQueries401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var folderID pgtype.UUID
	if request.Params.FolderId != nil {
		folderID.Bytes = *request.Params.FolderId
		folderID.Valid = true
	}

	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var spaceID pgtype.UUID
	if request.Params.SpaceId != nil {
		spaceID = pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}
	var queries []db.SavedQuery
	if folderID.Valid {
		queries, err = h.queries.ListVisibleSavedQueries(ctx, db.ListVisibleSavedQueriesParams{
			WorkspaceID: workspaceIDFromRequest(request.Id),
			UserID:      userID,
			SpaceID:     spaceID,
			FolderID:    folderID,
			Q:           request.Params.Q,
		})
	} else {
		queries, err = h.queries.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
			WorkspaceID: workspaceIDFromRequest(request.Id),
			UserID:      userID,
			SpaceID:     spaceID,
			Q:           request.Params.Q,
		})
	}
	if err != nil {
		return nil, err
	}

	versions, err := h.queries.ListSavedQueryVersions(ctx, workspaceIDFromRequest(request.Id))
	if err != nil {
		return nil, err
	}
	versionByID := make(map[string]int32, len(versions))
	for _, v := range versions {
		versionByID[uuidStr(v.EntityID)] = v.Version
	}

	result := make([]SavedQuery, 0, len(queries))
	for _, q := range queries {
		result = append(result, dbSavedQueryToPublic(q, versionByID[uuidStr(q.ID)]))
	}
	return ListSavedQueries200JSONResponse(result), nil
}

func (h *Handlers) CreateSavedQuery(ctx context.Context, request CreateSavedQueryRequestObject) (CreateSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return CreateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	createdBy, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	ws := workspaceIDFromRequest(request.Id)
	space, se, err := h.spaceForWrite(ctx, ws, request.Body.SpaceId, authz.SpaceEditor)
	if err != nil {
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return CreateSavedQuery403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return CreateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "space not found"}}, nil
	}

	var folderID pgtype.UUID
	if request.Body.FolderId != nil {
		folderID = pgtype.UUID{Bytes: *request.Body.FolderId, Valid: true}
		folder, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: folderID, WorkspaceID: ws})
		if err != nil || folder.SpaceID != space.ID {
			return CreateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
		}
	}

	slug := ""
	if request.Body.Slug != nil {
		slug = *request.Body.Slug
	}
	if slug == "" {
		slug, err = uniqueSlug(ctx, request.Body.Name, func(ctx context.Context, s string) (bool, error) {
			return h.queries.SavedQuerySlugExists(ctx, db.SavedQuerySlugExistsParams{WorkspaceID: ws, Slug: s})
		})
		if err != nil {
			return nil, err
		}
	} else if !slugRe.MatchString(slug) {
		return CreateSavedQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "slug must match ^[a-z][a-z0-9_]{0,62}$")}, nil
	}

	bindingID, targetSchema, te := h.savedQueryTarget(ctx, ws, request.Body.Target)
	if te != nil {
		code, message := "target_not_granted", "the target's catalog is not granted to this workspace"
		if te.code != "" {
			code, message = te.code, te.message
		}
		return CreateSavedQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(code, message)}, nil
	}

	query, err := h.contentService().CreateSavedQuery(ctx, createdBy, db.CreateSavedQueryParams{
		WorkspaceID:     ws,
		SpaceID:         space.ID,
		FolderID:        folderID,
		CreatedBy:       createdBy,
		UpdatedBy:       createdBy,
		Name:            request.Body.Name,
		Slug:            slug,
		Sql:             request.Body.Sql,
		TargetBindingID: bindingID,
		TargetSchema:    targetSchema,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return savedQueryConflict("a saved query with this name already exists in this workspace"), nil
		}
		return nil, err
	}

	return CreateSavedQuery201JSONResponse(dbSavedQueryToPublic(query, 1)), nil
}

func (h *Handlers) GetSavedQuery(ctx context.Context, request GetSavedQueryRequestObject) (GetSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var queryID pgtype.UUID
	queryID.Bytes = request.QueryId
	queryID.Valid = true

	ws := workspaceIDFromRequest(request.Id)
	query, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{WorkspaceID: ws, ID: queryID})
	if err != nil {
		return GetSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}
	_, se, err := h.requireSpace(ctx, ws, query.SpaceID, authz.SpaceViewer)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSavedQuery403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return GetSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	return GetSavedQuery200JSONResponse(dbSavedQueryToPublic(query, h.savedQueryVersion(ctx, ws, query.ID))), nil
}

func (h *Handlers) UpdateSavedQuery(ctx context.Context, request UpdateSavedQueryRequestObject) (UpdateSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return UpdateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	ws := workspaceIDFromRequest(request.Id)
	queryID := pgtype.UUID{Bytes: request.QueryId, Valid: true}
	current, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{WorkspaceID: ws, ID: queryID})
	if err != nil {
		return UpdateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	_, se, err := h.requireSpace(ctx, ws, current.SpaceID, authz.SpaceEditor)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return UpdateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return UpdateSavedQuery403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return UpdateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	spaceID := current.SpaceID
	if request.Body.SpaceId != nil {
		spaceID = pgtype.UUID{Bytes: *request.Body.SpaceId, Valid: true}
		if spaceID != current.SpaceID {
			_, se, err := h.requireSpace(ctx, ws, spaceID, authz.SpaceEditor)
			if err != nil {
				if errors.Is(err, authz.ErrUnauthenticated) {
					return UpdateSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
				}
				return nil, err
			}
			if se != nil {
				if se.status == 403 {
					return UpdateSavedQuery403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit the destination space"}}, nil
				}
				return UpdateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "destination space not found"}}, nil
			}
		}
	}

	name, sql := current.Name, current.Sql
	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Sql != nil {
		sql = *request.Body.Sql
	}

	folderID := current.FolderID
	if request.Body.MoveToRoot != nil && *request.Body.MoveToRoot {
		folderID = pgtype.UUID{}
	}
	if request.Body.FolderId != nil {
		folderID = pgtype.UUID{Bytes: *request.Body.FolderId, Valid: true}
	}
	if folderID.Valid {
		folder, err := h.queries.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: folderID, WorkspaceID: ws})
		if err != nil || folder.SpaceID != spaceID {
			return UpdateSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "folder not found"}}, nil
		}
	}

	slug := current.Slug
	if request.Body.Slug != nil {
		slug = *request.Body.Slug
		if !slugRe.MatchString(slug) {
			return UpdateSavedQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "slug must match ^[a-z][a-z0-9_]{0,62}$")}, nil
		}
	}

	bindingID, targetSchema := current.TargetBindingID, current.TargetSchema
	if request.Body.Target != nil {
		var te *targetError
		bindingID, targetSchema, te = h.savedQueryTarget(ctx, ws, request.Body.Target)
		if te != nil {
			code, message := "target_not_granted", "the target's catalog is not granted to this workspace"
			if te.code != "" {
				code, message = te.code, te.message
			}
			return UpdateSavedQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(code, message)}, nil
		}
	}

	updatedBy, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var expected *int32
	if request.Body.ExpectedVersion != nil {
		v := int32(*request.Body.ExpectedVersion)
		expected = &v
	}
	updated, _, err := h.contentService().UpdateSavedQuery(ctx, updatedBy, db.UpdateSavedQueryParams{
		WorkspaceID:     ws,
		ID:              queryID,
		Name:            name,
		Sql:             sql,
		FolderID:        folderID,
		SpaceID:         spaceID,
		Slug:            slug,
		TargetBindingID: bindingID,
		TargetSchema:    targetSchema,
		UpdatedBy:       updatedBy,
	}, expected)
	if err != nil {
		if isUniqueViolation(err) {
			return savedQueryUpdateConflict("a saved query with this name already exists in this workspace"), nil
		}
		if c, ok := h.versionConflictResponse(ctx, err); ok {
			return UpdateSavedQuery409JSONResponse{ConflictJSONResponse: c}, nil
		}
		return nil, err
	}

	return UpdateSavedQuery200JSONResponse(dbSavedQueryToPublic(updated, h.savedQueryVersion(ctx, ws, queryID))), nil
}

func (h *Handlers) DeleteSavedQuery(ctx context.Context, request DeleteSavedQueryRequestObject) (DeleteSavedQueryResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return DeleteSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	ws := workspaceIDFromRequest(request.Id)
	queryID := pgtype.UUID{Bytes: request.QueryId, Valid: true}
	current, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{WorkspaceID: ws, ID: queryID})
	if err != nil {
		return DeleteSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}
	_, se, err := h.requireSpace(ctx, ws, current.SpaceID, authz.SpaceEditor)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSavedQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return DeleteSavedQuery403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return DeleteSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
	}

	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.contentService().DeleteSavedQuery(ctx, actor, ws, queryID); err != nil {
		if errors.Is(err, content.ErrNotFound) {
			return DeleteSavedQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
		}
		return nil, err
	}

	return DeleteSavedQuery204Response{}, nil
}

// ── Executions ────────────────────────────────────────────────────────────────

const (
	defaultExecutionPage = 20
	maxExecutionPage     = 100
)

func (h *Handlers) ListExecutions(ctx context.Context, request ListExecutionsRequestObject) (ListExecutionsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListExecutions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	caller := auth.UserIDFromContext(ctx)
	callerID, err := uuidFromString(caller)
	if err != nil {
		return ListExecutions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	p := request.Params
	limit := defaultExecutionPage
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxExecutionPage {
			return ListExecutions422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "limit must be between 1 and 100")}, nil
		}
		limit = *p.Limit
	}

	// Visibility (#36): the workspace owner and global admins see every
	// execution; everyone else sees only their own.
	seeAll := auth.RoleFromContext(ctx) == "admin"
	if !seeAll {
		owner, err := h.queries.IsWorkspaceOwner(ctx, db.IsWorkspaceOwnerParams{
			ID:     workspaceIDFromRequest(request.Id),
			UserID: callerID,
		})
		if err != nil {
			return nil, err
		}
		seeAll = owner
	}

	userFilter := pgtype.UUID{}
	if p.UserId != nil && *p.UserId != "" {
		if *p.UserId == "me" {
			userFilter = callerID
		} else {
			uid, err := uuidFromString(*p.UserId)
			if err != nil {
				return ListExecutions422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "invalid user_id")}, nil
			}
			userFilter = uid
		}
	}
	if !seeAll {
		userFilter = callerID
	}

	params := db.ListExecutionHistoryParams{
		WorkspaceID: workspaceIDFromRequest(request.Id),
		UserID:      userFilter,
		Q:           p.Q,
		Lim:         int32(limit + 1),
	}
	if p.Status != nil {
		status := string(*p.Status)
		params.Status = &status
	}
	if p.From != nil {
		params.FromTime = pgtype.Timestamptz{Time: *p.From, Valid: true}
	}
	if p.To != nil {
		params.ToTime = pgtype.Timestamptz{Time: *p.To, Valid: true}
	}
	if p.CatalogId != nil {
		params.CatalogID = pgtype.UUID{Bytes: *p.CatalogId, Valid: true}
	}
	if p.EngineId != nil {
		params.EngineID = pgtype.UUID{Bytes: *p.EngineId, Valid: true}
	}
	if p.Cursor != nil && *p.Cursor != "" {
		at, id, ok := decodeHistoryCursor(*p.Cursor)
		if !ok {
			return ListExecutions422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "invalid cursor")}, nil
		}
		params.CursorAt = pgtype.Timestamptz{Time: at, Valid: true}
		params.CursorID = id
	}

	rows, err := h.queries.ListExecutionHistory(ctx, params)
	if err != nil {
		return nil, err
	}
	out := ListExecutions200JSONResponse{Data: make([]Execution, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		cursor := encodeHistoryCursor(rows[limit-1].StartedAt.Time, rows[limit-1].ID)
		out.NextCursor = &cursor
	}
	for _, row := range rows {
		out.Data = append(out.Data, historyRowToPublic(row))
	}
	return out, nil
}

func encodeHistoryCursor(at time.Time, id pgtype.UUID) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(at.UTC().Format(time.RFC3339Nano) + "|" + uuid.UUID(id.Bytes).String()),
	)
}

func decodeHistoryCursor(cursor string) (time.Time, pgtype.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, pgtype.UUID{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	return at, pgtype.UUID{Bytes: [16]byte(id), Valid: true}, true
}

// historyRowToPublic maps one joined history row, including the runner.
func historyRowToPublic(row db.ListExecutionHistoryRow) Execution {
	uid := openapi_types.UUID(row.UserID.Bytes)
	name := row.UserName
	sql := row.Sql
	exec := Execution{
		Id:          openapi_types.UUID(row.ID.Bytes),
		WorkspaceId: openapi_types.UUID(row.WorkspaceID.Bytes),
		UserId:      &uid,
		UserName:    &name,
		Status:      ExecutionStatus(row.Status),
		Sql:         sql,
		Target:      executionTargetFrom(row.TargetSnapshot, row.EngineID, row.TargetBindingID, row.TargetCatalogID),
	}
	if row.SavedQueryID.Valid {
		sqid := openapi_types.UUID(row.SavedQueryID.Bytes)
		exec.SavedQueryId = &sqid
	}
	if row.EngineQueryID != nil {
		exec.EngineQueryId = row.EngineQueryID
	}
	if row.RowsReturned != nil {
		rows := int(*row.RowsReturned)
		exec.RowsReturned = &rows
	}
	if row.DurationMs != nil {
		duration := int(*row.DurationMs)
		exec.DurationMs = &duration
	}
	if row.StartedAt.Valid {
		started := row.StartedAt.Time
		exec.StartedAt = &started
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time
		exec.FinishedAt = &finished
	}
	return exec
}

func (h *Handlers) RunQuery(ctx context.Context, request RunQueryRequestObject) (RunQueryResponseObject, error) {
	ev := audit.Event{Action: audit.ExecutionCreate, ResourceType: "execution", WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return RunQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return nil, err
	}

	var wsID pgtype.UUID
	wsID.Bytes = request.Id
	wsID.Valid = true

	workspace, err := h.queries.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return RunQuery422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "workspace not found",
			},
		}, nil
	}

	// #111: rerun copies the original statements on the server, never re-splits
	// SQL. The target defaults to the original one unless the request names one.
	var rerunExecutionID string
	var rerunFromPosition int32
	if request.Body.Rerun != nil {
		originalID := pgtype.UUID{Bytes: request.Body.Rerun.ExecutionId, Valid: true}
		original, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
			ID:          originalID,
			WorkspaceID: workspaceIDFromRequest(request.Id),
		})
		if err != nil {
			h.audited(ctx, ev.Failed(audit.ReasonNotFound))
			return RunQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
		}
		if original.UserID.Bytes != uid.Bytes && (!workspace.OwnerUserID.Valid || workspace.OwnerUserID.Bytes != uid.Bytes) {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return RunQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
		}
		rerunExecutionID = openapi_types.UUID(original.ID.Bytes).String()
		rerunFromPosition = int32(request.Body.Rerun.FromPosition)
		request.Body.Sql = original.Sql
		if request.Body.Target == nil && original.TargetBindingID.Valid {
			binding := openapi_types.UUID(original.TargetBindingID.Bytes)
			request.Body.Target = &TargetRequest{BindingId: &binding, Schema: original.TargetSchema}
		}
	}

	target, err := h.ResolveTarget(ctx, workspace, request.Body.Target)
	if te, ok := asTargetError(err); ok {
		if te.notFound {
			h.audited(ctx, ev.Denied(audit.ReasonNotFound))
			return RunQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: te.message}}, nil
		}
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return RunQuery422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
	}
	if err != nil {
		return nil, err
	}
	// A target runs where an adapter for its engine kind exists; direct
	// engines get theirs in #81-#83.
	if !slices.Contains(queryengine.Kinds(), target.EngineKind) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return RunQuery422JSONResponse{UnprocessableEntityJSONResponse: (&targetError{
			code:    "engine_unsupported",
			message: "queries on " + target.EngineKind + " engines are not available yet",
		}).unprocessable()}, nil
	}
	snapshot, err := target.Snapshot()
	if err != nil {
		return nil, err
	}
	trinoCatalog := target.EngineCatalog
	var targetSchema *string
	if target.Schema != "" {
		targetSchema = &target.Schema
	}

	var savedQueryID pgtype.UUID
	if request.Body.SavedQueryId != nil {
		savedQueryID.Bytes = *request.Body.SavedQueryId
		savedQueryID.Valid = true
		// A saved query from another workspace, or from a space the caller
		// cannot see, must not be attached to an execution (it would leak its
		// id into this workspace's history).
		saved, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{
			ID: savedQueryID, WorkspaceID: workspaceIDFromRequest(request.Id),
		})
		if err != nil {
			h.audited(ctx, ev.Failed(audit.ReasonNotFound))
			return RunQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
		}
		if _, se, err := h.requireSpace(ctx, workspaceIDFromRequest(request.Id), saved.SpaceID, authz.SpaceViewer); err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return RunQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
			}
			return nil, err
		} else if se != nil {
			h.audited(ctx, ev.Failed(audit.ReasonForbidden))
			return RunQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "saved query not found"}}, nil
		}
	}

	// Correlate the execution with the inbound HTTP request so the row, the
	// Temporal workflow, and the activity logs can be joined back to the
	// request log line.
	requestID := middleware.GetReqID(ctx)
	var requestIDPtr *string
	if requestID != "" {
		requestIDPtr = &requestID
	}

	// Per-user Trino identity: user SQL reaches Trino as the Baleia user, never
	// as the admin or service identity.
	trinoUser, err := h.trinoIdentityFor(ctx, uid)
	if err != nil {
		return nil, err
	}

	execution, err := h.queries.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID:  workspaceIDFromRequest(request.Id),
		UserID:       uid,
		SavedQueryID: savedQueryID,
		Status:       "running",
		Sql:          request.Body.Sql,
		TrinoCatalog: &trinoCatalog,
		RequestID:    requestIDPtr,
		// The resolved target, recorded for history (#13).
		EngineID:        target.EngineID,
		TargetBindingID: target.BindingID,
		TargetCatalogID: target.CatalogID,
		TargetSchema:    targetSchema,
		TargetSnapshot:  snapshot,
	})
	if err != nil {
		return nil, err
	}

	executionID := openapi_types.UUID(execution.ID.Bytes).String()

	wo := client.StartWorkflowOptions{
		ID:        "exec-" + executionID,
		TaskQueue: "baleia-task-queue",
	}

	_, err = h.temporalClient.ExecuteWorkflow(ctx, wo, workflows.QueryExecutionWorkflowV2, workflows.QueryExecutionInput{
		ExecutionID:       executionID,
		WorkspaceID:       request.Id.String(),
		SQL:               request.Body.Sql,
		Target:            target,
		Identity:          queryengine.Identity{UserID: trinoUser},
		StatementTimeout:  h.cfg.StatementTimeout,
		RequestID:         requestID,
		ExecutionTimeout:  h.cfg.ExecutionTimeout,
		RerunExecutionID:  rerunExecutionID,
		RerunFromPosition: rerunFromPosition,
	})
	if err != nil {
		// The workflow failed to start; fail the row so it does not stay
		// `running` forever. Use a context detached from the request in case
		// the request was cancelled mid-call.
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, ferr := h.queries.UpdateQueryExecution(failCtx, db.UpdateQueryExecutionParams{
			ID:     execution.ID,
			Status: "failed",
		}); ferr != nil && !errors.Is(ferr, pgx.ErrNoRows) {
			slog.Error("failed to mark execution failed after workflow start error",
				"execution_id", executionID, "start_err", err, "mark_err", ferr)
		}
		return nil, err
	}

	// The target, never the SQL text.
	ev = withID(ev, execution.ID)
	ev.Changes = map[string]audit.Change{"target": {To: map[string]any{
		"catalog": target.CatalogName, "engine": target.EngineName, "engine_kind": target.EngineKind, "schema": target.Schema, "read_only": target.ReadOnly,
	}}}
	h.audited(ctx, ev.Allowed())
	return RunQuery202JSONResponse(dbExecutionToPublic(execution)), nil
}

func (h *Handlers) GetExecution(ctx context.Context, request GetExecutionRequestObject) (GetExecutionResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	execution, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
		ID:          executionID,
		WorkspaceID: workspaceIDFromRequest(request.Id),
	})
	if err != nil {
		return GetExecution404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	return GetExecution200JSONResponse(dbExecutionToPublic(execution)), nil
}

func (h *Handlers) CancelExecution(ctx context.Context, request CancelExecutionRequestObject) (CancelExecutionResponseObject, error) {
	ev := audit.Event{Action: audit.ExecutionCancel, ResourceType: "execution", ResourceID: uuid.UUID(request.ExecutionId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return CancelExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true
	wsID := workspaceIDFromRequest(request.Id)

	// Conditional cancel: only a running execution transitions to cancelled, so
	// a completion or failure that races after this call cannot be overwritten.
	updated, err := h.queries.CancelQueryExecution(ctx, db.CancelQueryExecutionParams{
		ID:          executionID,
		WorkspaceID: wsID,
	})
	if err == nil {
		// Cancel the Temporal workflow so the running statement stops and its
		// Trino query is killed. A NotFound means the workflow already ended.
		cancelErr := h.temporalClient.CancelWorkflow(ctx, "exec-"+executionID.String(), "")
		var notFoundErr *serviceerror.NotFound
		if cancelErr != nil && !errors.As(cancelErr, &notFoundErr) {
			slog.Warn("failed to cancel temporal workflow", "execution_id", executionID.String(), "err", cancelErr)
		}
		// The worker running it may be gone: stop the statement on its engine
		// through the persisted handle too. Cancelling a finished statement is
		// not an error.
		h.cancelOnEngine(ctx, updated)
		h.audited(ctx, ev.Allowed())
		return CancelExecution200JSONResponse(dbExecutionToPublic(updated)), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// Nothing was cancelled: the execution is gone, foreign, or already
	// terminal. Return its current scoped state, or 404 when it is absent.
	current, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
		ID:          executionID,
		WorkspaceID: wsID,
	})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return CancelExecution404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}
	// Already finished: nothing was cancelled.
	h.audited(ctx, ev.Failed(audit.ReasonConflict))
	return CancelExecution200JSONResponse(dbExecutionToPublic(current)), nil
}

// cancelOnEngine cancels the running statements of an execution by their
// persisted handles, best effort and bounded in time.
func (h *Handlers) cancelOnEngine(ctx context.Context, e db.QueryExecution) {
	if h.engines == nil {
		return
	}
	rows, err := h.queries.ListRunningStatementHandles(ctx, e.ID)
	if err != nil || len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var eng queryengine.Engine
	if e.EngineID.Valid {
		eng, err = h.engines.Get(ctx, e.EngineID)
	} else {
		eng, err = h.engines.DefaultTrino(ctx)
	}
	if err != nil {
		slog.Warn("cancel on engine: no engine", "execution_id", e.ID.String(), "err", err)
		return
	}
	for _, r := range rows {
		var handle queryengine.QueryHandle
		if json.Unmarshal(r.QueryHandle, &handle) != nil {
			continue
		}
		if err := eng.Cancel(ctx, handle); err != nil {
			slog.Warn("cancel on engine failed", "execution_id", e.ID.String(), "position", r.Position, "err", err)
		}
	}
}

func (h *Handlers) ListStatements(ctx context.Context, request ListStatementsRequestObject) (ListStatementsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListStatements401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true
	wsID := workspaceIDFromRequest(request.Id)

	// The join-based statements query returns an empty slice for a foreign or
	// absent execution, which would hide a cross-workspace id behind a 200.
	// Verify the execution belongs to this workspace first so we can answer 404.
	if _, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
		ID:          executionID,
		WorkspaceID: wsID,
	}); err != nil {
		return ListStatements404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	statements, err := h.queries.ListQueryStatementsByWorkspace(ctx, db.ListQueryStatementsByWorkspaceParams{
		ExecutionID: executionID,
		WorkspaceID: wsID,
	})
	if err != nil {
		return nil, err
	}

	result := make([]QueryStatement, 0, len(statements))
	for _, s := range statements {
		result = append(result, dbStatementToPublic(s))
	}

	return ListStatements200JSONResponse(result), nil
}

func (h *Handlers) GetResults(ctx context.Context, request GetResultsRequestObject) (GetResultsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return GetResults401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	execution, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
		ID:          executionID,
		WorkspaceID: workspaceIDFromRequest(request.Id),
	})
	if err != nil {
		return GetResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "execution not found"}}, nil
	}

	if execution.Status != "finished" {
		return GetResults409JSONResponse{
			Code:    "conflict",
			Message: "execution not yet completed",
		}, nil
	}

	if execution.ResultKey == nil || *execution.ResultKey == "" {
		return GetResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "no results available"}}, nil
	}

	reader, contentType, err := h.s3.GetReader(ctx, *execution.ResultKey)
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/vnd.apache.arrow.stream"
	}

	return arrowStreamResponse{reader: reader, contentType: contentType}, nil
}

type arrowStreamResponse struct {
	reader      io.ReadCloser
	contentType string
	// disposition is the Content-Disposition header for an export; empty for
	// the viewer's inline Arrow stream.
	disposition string
	// totalRows is the statement's full row count for a paged read; nil when the
	// whole result is streamed.
	totalRows *int64
}

func (r arrowStreamResponse) VisitGetResultsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r arrowStreamResponse) VisitGetStatementResultsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r arrowStreamResponse) write(w http.ResponseWriter) error {
	defer r.reader.Close()
	w.Header().Set("Content-Type", r.contentType)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	if r.totalRows != nil {
		w.Header().Set("X-Total-Rows", strconv.FormatInt(*r.totalRows, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, r.reader)
	return err
}

// GetStatementResults streams a single statement's result set, scoped to the
// requesting workspace. Same auth and delivery model as GetResults.
func (h *Handlers) GetStatementResults(ctx context.Context, request GetStatementResultsRequestObject) (GetStatementResultsResponseObject, error) {
	ev := audit.Event{Action: audit.ResultsDownload, ResourceType: "execution", ResourceID: uuid.UUID(request.ExecutionId).String(), WorkspaceID: request.Id.String()}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		h.audited(ctx, ev.Denied(audit.ReasonNotFound))
		return GetStatementResults401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "workspace not found"}}, nil
	}

	var executionID pgtype.UUID
	executionID.Bytes = request.ExecutionId
	executionID.Valid = true

	statement, err := h.queries.GetQueryStatementByPosition(ctx, db.GetQueryStatementByPositionParams{
		ExecutionID: executionID,
		WorkspaceID: workspaceIDFromRequest(request.Id),
		Position:    int32(request.Position),
	})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return GetStatementResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "statement not found"}}, nil
	}

	if statement.Status != "finished" {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return GetStatementResults409JSONResponse{
			Code:    "conflict",
			Message: "statement has no result yet",
		}, nil
	}
	resultIndex := 0
	if request.Params.ResultIndex != nil {
		resultIndex = *request.Params.ResultIndex
	}
	if resultIndex < 0 {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return GetStatementResults422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "result_index must be zero or greater")}, nil
	}
	format := exportArrow
	if request.Params.Format != nil {
		parsed, ok := parseExportFormat(*request.Params.Format)
		if !ok {
			h.audited(ctx, ev.Failed(audit.ReasonValidation))
			return GetStatementResults422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "format must be arrow, csv, tsv or jsonl")}, nil
		}
		format = parsed
	}

	offset, limit := 0, 0
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if offset < 0 || limit < 0 {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return GetStatementResults422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "offset and limit must be zero or greater")}, nil
	}

	resultKey := resultKeyFor(statement.ResultSets, statement.ResultKey, resultIndex)
	if resultKey == "" {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return GetStatementResults404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "statement produced no result set"}}, nil
	}

	disposition := fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("%s-%d.%s", request.ExecutionId, request.Position, format.extension()))

	// Paged read of the stored Arrow object: seek to the covering batches and
	// re-emit only the requested rows, so the client never downloads the whole
	// result (#238). The full row count travels in X-Total-Rows.
	if format == exportArrow && limit > 0 {
		page, err := h.pageOf(ctx, resultKey, int64(offset), int64(limit))
		if err != nil {
			return nil, err
		}
		totalRows := int64(0)
		if statement.RowsReturned != nil {
			totalRows = *statement.RowsReturned
		}
		h.audited(ctx, ev.Allowed())
		return arrowStreamResponse{
			reader:      io.NopCloser(bytes.NewReader(page)),
			contentType: "application/vnd.apache.arrow.stream",
			disposition: disposition,
			totalRows:   &totalRows,
		}, nil
	}

	reader, contentType, err := h.s3.GetReader(ctx, resultKey)
	if err != nil {
		return nil, err
	}
	if contentType == "" || format != exportArrow {
		contentType = format.contentType()
	}

	h.audited(ctx, ev.Allowed())
	if format == exportArrow {
		return arrowStreamResponse{reader: reader, contentType: contentType, disposition: disposition}, nil
	}

	// Convert as the object streams out of storage; the pipe keeps memory
	// bounded by one record batch.
	pr, pw := io.Pipe()
	go func() {
		defer reader.Close()
		pw.CloseWithError(convertResult(pw, reader, format))
	}()
	return arrowStreamResponse{reader: pr, contentType: contentType, disposition: disposition}, nil
}

// pageOf returns rows [offset, offset+limit) of a stored result. New results are
// Arrow files (seek by footer, no full download); legacy stream results are
// decoded and sliced.
func (h *Handlers) pageOf(ctx context.Context, key string, offset, limit int64) ([]byte, error) {
	if ra, err := h.s3.NewReaderAt(ctx, key); err == nil {
		if page, serr := arrowipc.SliceRows(ra, offset, limit); serr == nil {
			return page, nil
		}
	}
	data, _, err := h.s3.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return arrowipc.SliceStream(data, offset, limit)
}

// IssueWsTicket mints a single-use WebSocket ticket (#26) so the client never
// puts the JWT in a URL.
func (h *Handlers) IssueWsTicket(ctx context.Context, request IssueWsTicketRequestObject) (IssueWsTicketResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	if userID == "" {
		return IssueWsTicket401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "not authenticated"}}, nil
	}
	executionID := ""
	if request.Body != nil && request.Body.ExecutionId != nil {
		executionID = request.Body.ExecutionId.String()
	}
	ticket, expiresAt := h.wsTicketStore().Issue(userID, executionID)
	return IssueWsTicket201JSONResponse{Ticket: ticket, ExpiresAt: expiresAt}, nil
}

func (h *Handlers) StreamExecution(ctx context.Context, request StreamExecutionRequestObject) (StreamExecutionResponseObject, error) {
	return StreamExecution401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "websocket not yet implemented"}}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// trinoIdentityFor maps a Baleia user to the Trino identity used for their
// queries, according to BALEIA_TRINO_USER_IDENTITY ("uuid" default: u_<uuid>,
// "email": the user's e-mail). Never an admin or service identity.
func (h *Handlers) trinoIdentityFor(ctx context.Context, uid pgtype.UUID) (string, error) {
	return trinoIdentity(ctx, h.queries, h.cfg.TrinoUserIdentity, uid)
}

// trinoIdentity is trinoIdentityFor without the Handlers, for the Lens handlers.
func trinoIdentity(ctx context.Context, q db.Querier, mode string, uid pgtype.UUID) (string, error) {
	switch mode {
	case "email":
		return trinoidentity.For(ctx, q, mode, uid)
	default:
		// The API derives the uuid identity from the token without a lookup.
		return "u_" + uid.String(), nil
	}
}

// savedQueryTarget validates the optional target of a saved query: the binding
// must be a target of the workspace (its catalog granted, #16).
func (h *Handlers) savedQueryTarget(ctx context.Context, ws pgtype.UUID, t *SavedQueryTarget) (pgtype.UUID, *string, *targetError) {
	if t == nil || t.BindingId == nil {
		return pgtype.UUID{}, nil, nil
	}
	workspace, err := h.queries.GetWorkspaceByID(ctx, ws)
	if err != nil {
		return pgtype.UUID{}, nil, &targetError{code: "workspace_not_found", message: "workspace not found"}
	}
	target, err := h.ResolveTarget(ctx, workspace, &TargetRequest{BindingId: t.BindingId, Schema: t.Schema})
	if err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return pgtype.UUID{}, nil, &targetError{code: "target_not_granted", message: "the target's catalog is not granted to this workspace"}
			}
			return pgtype.UUID{}, nil, te
		}
		return pgtype.UUID{}, nil, &targetError{code: "internal", message: err.Error()}
	}
	return target.BindingID, t.Schema, nil
}

func dbFolderToPublic(f db.QueryFolder) QueryFolder {
	qf := QueryFolder{
		Id:          openapi_types.UUID(f.ID.Bytes),
		WorkspaceId: openapi_types.UUID(f.WorkspaceID.Bytes),
		SpaceId:     openapi_types.UUID(f.SpaceID.Bytes),
		Slug:        f.Slug,
		Name:        f.Name,
		CreatedAt:   pgTimestampToTime(f.CreatedAt),
	}
	if f.ParentID.Valid {
		pid := openapi_types.UUID(f.ParentID.Bytes)
		qf.ParentId = &pid
	}
	return qf
}

func dbSavedQueryToPublic(q db.SavedQuery, version int32) SavedQuery {
	sq := SavedQuery{
		Id:          openapi_types.UUID(q.ID.Bytes),
		WorkspaceId: openapi_types.UUID(q.WorkspaceID.Bytes),
		SpaceId:     openapi_types.UUID(q.SpaceID.Bytes),
		Slug:        q.Slug,
		CreatedBy:   openapi_types.UUID(q.CreatedBy.Bytes),
		Name:        q.Name,
		Sql:         q.Sql,
		Version:     int(version),
		CreatedAt:   pgTimestampToTime(q.CreatedAt),
		UpdatedAt:   timePtr(q.UpdatedAt),
	}
	if q.FolderID.Valid {
		fid := openapi_types.UUID(q.FolderID.Bytes)
		sq.FolderId = &fid
	}
	if q.UpdatedBy.Valid {
		by := openapi_types.UUID(q.UpdatedBy.Bytes)
		sq.UpdatedBy = &by
	}
	if q.TargetBindingID.Valid || q.TargetSchema != nil {
		sq.Target = &SavedQueryTarget{Schema: q.TargetSchema}
		if q.TargetBindingID.Valid {
			bid := openapi_types.UUID(q.TargetBindingID.Bytes)
			sq.Target.BindingId = &bid
		}
	}
	return sq
}

// savedQueryVersion returns the latest content version, or 0 when none.
func (h *Handlers) savedQueryVersion(ctx context.Context, ws, id pgtype.UUID) int32 {
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySavedQuery, EntityID: id,
	})
	if err != nil {
		return 0
	}
	return v.Version
}

// versionConflictResponse maps content.ErrVersionConflict to the 409 body with
// the current version and who wrote it.
func (h *Handlers) versionConflictResponse(ctx context.Context, err error) (ConflictJSONResponse, bool) {
	var vc *content.VersionConflict
	if !errors.As(err, &vc) {
		return ConflictJSONResponse{}, false
	}
	params := map[string]interface{}{
		"current_version": vc.Current,
		"updated_at":      vc.UpdatedAt,
	}
	if vc.UpdatedBy.Valid {
		by := map[string]interface{}{"id": uuidStr(vc.UpdatedBy)}
		if user, uerr := h.queries.GetUserByID(ctx, vc.UpdatedBy); uerr == nil {
			by["name"] = user.Name
		}
		params["updated_by"] = by
	}
	return ConflictJSONResponse{
		Code:    "version_conflict",
		Message: "the content changed since you loaded it",
		Params:  &params,
	}, true
}

func dbExecutionToPublic(e db.QueryExecution) Execution {
	id := openapi_types.UUID(e.ID.Bytes)
	workspaceID := openapi_types.UUID(e.WorkspaceID.Bytes)
	status := ExecutionStatus(e.Status)

	exec := Execution{
		Id:          id,
		WorkspaceId: workspaceID,
		Status:      status,
		Sql:         e.Sql,
	}
	if e.SavedQueryID.Valid {
		sqid := openapi_types.UUID(e.SavedQueryID.Bytes)
		exec.SavedQueryId = &sqid
	}
	if e.EngineQueryID != nil {
		exec.EngineQueryId = e.EngineQueryID
	}
	if e.RowsReturned != nil {
		rows := int(*e.RowsReturned)
		exec.RowsReturned = &rows
	}
	if e.DurationMs != nil {
		dur := int(*e.DurationMs)
		exec.DurationMs = &dur
	}
	if e.StartedAt.Valid {
		started := e.StartedAt.Time
		exec.StartedAt = &started
	}
	if e.FinishedAt.Valid {
		finished := e.FinishedAt.Time
		exec.FinishedAt = &finished
	}
	exec.Target = executionTargetFrom(e.TargetSnapshot, e.EngineID, e.TargetBindingID, e.TargetCatalogID)
	if exec.Target != nil {
		exec.ReadOnly = exec.Target.ReadOnly
	}
	return exec
}

func executionTarget(e db.QueryExecution) *ExecutionTarget {
	return executionTargetFrom(e.TargetSnapshot, e.EngineID, e.TargetBindingID, e.TargetCatalogID)
}

// executionTargetFrom is the target an execution recorded when it started: the
// ids (null once the binding, catalog or engine is gone) and the snapshot.
func executionTargetFrom(snapshot []byte, engineID, bindingID, catalogID pgtype.UUID) *ExecutionTarget {
	if len(snapshot) == 0 {
		return nil
	}
	var t ExecutionTarget
	if err := json.Unmarshal(snapshot, &t); err != nil {
		return nil
	}
	uuidOrNil := func(u pgtype.UUID) *openapi_types.UUID {
		if !u.Valid {
			return nil
		}
		v := openapi_types.UUID(u.Bytes)
		return &v
	}
	t.EngineId, t.BindingId, t.CatalogId = uuidOrNil(engineID), uuidOrNil(bindingID), uuidOrNil(catalogID)
	return &t
}

func dbStatementToPublic(s db.QueryStatement) QueryStatement {
	id := openapi_types.UUID(s.ID.Bytes)
	execID := openapi_types.UUID(s.ExecutionID.Bytes)

	qs := QueryStatement{
		Id:          id,
		ExecutionId: execID,
		Position:    int(s.Position),
		Sql:         s.Sql,
		Status:      QueryStatementStatus(s.Status),
	}
	if s.EngineQueryID != nil {
		qs.EngineQueryId = s.EngineQueryID
	}
	var sets []struct {
		Bytes       int  `json:"bytes"`
		ResultIndex int  `json:"result_index"`
		Rows        int  `json:"rows"`
		Truncated   bool `json:"truncated"`
	}
	if len(s.ResultSets) > 0 && json.Unmarshal(s.ResultSets, &sets) == nil {
		qs.ResultSets = &sets
	}
	count := len(sets)
	qs.ResultCount = &count
	if s.StartedAt.Valid {
		started := s.StartedAt.Time
		qs.StartedAt = &started
	}
	if s.FinishedAt.Valid {
		finished := s.FinishedAt.Time
		qs.FinishedAt = &finished
	}
	if s.RowsReturned != nil {
		rows := int(*s.RowsReturned)
		qs.RowsReturned = &rows
	}
	if s.DurationMs != nil {
		dur := int(*s.DurationMs)
		qs.DurationMs = &dur
	}
	if s.ResultBytes != nil {
		sz := int(*s.ResultBytes)
		qs.ResultBytes = &sz
	}
	if s.Truncated {
		tr := true
		qs.Truncated = &tr
	}
	if s.ErrorMessage != nil {
		qs.ErrorMessage = s.ErrorMessage
	}
	qs.ErrorCode = s.ErrorCode
	return qs
}

// ── Helpers ─────────────────────────────────────────────────────────────────────

type savedQueryConflictJSONResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r savedQueryConflictJSONResponse) VisitCreateSavedQueryResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	return json.NewEncoder(w).Encode(r)
}

func (r savedQueryConflictJSONResponse) VisitUpdateSavedQueryResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	return json.NewEncoder(w).Encode(r)
}

func savedQueryConflict(msg string) savedQueryConflictJSONResponse {
	return savedQueryConflictJSONResponse{Code: "conflict", Message: msg}
}

func savedQueryUpdateConflict(msg string) savedQueryConflictJSONResponse {
	return savedQueryConflictJSONResponse{Code: "conflict", Message: msg}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
