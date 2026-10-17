package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Content versions and trash (#34). The space role is checked on the entity's
// current space, or on the last version's space for deleted entities.

const (
	defaultVersionPage = 50
	maxVersionPage     = 200
)

func versionPage(limit, offset *int) (int32, int32) {
	l, o := defaultVersionPage, 0
	if limit != nil {
		if *limit < 1 {
			l = 1
		} else if *limit > maxVersionPage {
			l = maxVersionPage
		} else {
			l = *limit
		}
	}
	if offset != nil && *offset > 0 {
		o = *offset
	}
	return int32(l), int32(o)
}

func pgUUIDFromPtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// savedQuerySpaceForHistory returns the space to authorize against: the current
// entity's if it still exists, else the last version's.
func (h *Handlers) savedQuerySpaceForHistory(ctx context.Context, ws, id pgtype.UUID) (pgtype.UUID, error) {
	q, err := h.queries.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{ID: id, WorkspaceID: ws})
	if err == nil {
		return q.SpaceID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySavedQuery, EntityID: id,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return v.SpaceID, nil
}

func (h *Handlers) metricSpaceForHistory(ctx context.Context, ws, id pgtype.UUID) (pgtype.UUID, error) {
	m, err := h.queries.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: ws, ID: id})
	if err == nil {
		return m.SpaceID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticMetric, EntityID: id,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return v.SpaceID, nil
}

// unauthorized maps an authz/unauthenticated error to the endpoint's 401.
func unauthorized(err error) bool { return errors.Is(err, authz.ErrUnauthenticated) }

// ── saved query versions ───────────────────────────────────────────────────────

func (h *Handlers) ListSavedQueryVersions(ctx context.Context, request ListSavedQueryVersionsRequestObject) (ListSavedQueryVersionsResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.QueryId, Valid: true}
	spaceID, err := h.savedQuerySpaceForHistory(ctx, ws, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListSavedQueryVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("saved query not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return ListSavedQueryVersions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return ListSavedQueryVersions403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return ListSavedQueryVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("saved query not found")}, nil
	}
	lim, off := versionPage(request.Params.Limit, request.Params.Offset)
	rows, err := h.queries.ListContentVersions(ctx, db.ListContentVersionsParams{
		WorkspaceID: ws, EntityType: content.EntitySavedQuery, EntityID: id, Lim: lim, Off: off,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSavedQueryVersions200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, contentVersionToPublic(r.Version, r.ChangeKind, r.AuthorID, r.AuthorName, r.RestoredFrom, r.CreatedAt))
	}
	return out, nil
}

func (h *Handlers) GetSavedQueryVersion(ctx context.Context, request GetSavedQueryVersionRequestObject) (GetSavedQueryVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.QueryId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySavedQuery, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return GetSavedQueryVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSavedQueryVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return GetSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	detail, err := contentVersionDetail(v.Version, v.ChangeKind, v.AuthorID, v.AuthorName, v.RestoredFrom, v.CreatedAt, v.Snapshot)
	if err != nil {
		return nil, err
	}
	return GetSavedQueryVersion200JSONResponse(detail), nil
}

func (h *Handlers) RestoreSavedQueryVersion(ctx context.Context, request RestoreSavedQueryVersionRequestObject) (RestoreSavedQueryVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.QueryId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySavedQuery, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RestoreSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceEditor); err != nil {
		if unauthorized(err) {
			return RestoreSavedQueryVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return RestoreSavedQueryVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return RestoreSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}

	var spaceID *pgtype.UUID
	if request.Body != nil && request.Body.SpaceId != nil {
		dest := pgtype.UUID{Bytes: *request.Body.SpaceId, Valid: true}
		if se, err := h.contentSpace(ctx, ws, dest, authz.SpaceEditor); err != nil {
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return RestoreSavedQueryVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit the destination space"}}, nil
			}
			return RestoreSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("destination space not found")}, nil
		}
		spaceID = &dest
	}

	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var newName *string
	if request.Body != nil {
		newName = request.Body.NewName
	}
	q, err := h.contentService().RestoreSavedQuery(ctx, actor, ws, id, int32(request.Version), newName, spaceID)
	switch {
	case errors.Is(err, content.ErrNameConflict):
		return RestoreSavedQueryVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "name_conflict", Message: "another saved query already uses this name or slug"}}, nil
	case errors.Is(err, content.ErrSpaceMissing):
		return RestoreSavedQueryVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "space_missing", Message: "the version's space no longer exists; pass space_id"}}, nil
	case errors.Is(err, content.ErrNotFound):
		return RestoreSavedQueryVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	case err != nil:
		return nil, err
	}
	return RestoreSavedQueryVersion200JSONResponse(dbSavedQueryToPublic(q, h.savedQueryVersion(ctx, ws, q.ID))), nil
}

// ── semantic metric versions ───────────────────────────────────────────────────

func (h *Handlers) ListSemanticMetricVersions(ctx context.Context, request ListSemanticMetricVersionsRequestObject) (ListSemanticMetricVersionsResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	spaceID, err := h.metricSpaceForHistory(ctx, ws, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListSemanticMetricVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return ListSemanticMetricVersions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return ListSemanticMetricVersions403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return ListSemanticMetricVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	lim, off := versionPage(request.Params.Limit, request.Params.Offset)
	rows, err := h.queries.ListContentVersions(ctx, db.ListContentVersionsParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticMetric, EntityID: id, Lim: lim, Off: off,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticMetricVersions200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, contentVersionToPublic(r.Version, r.ChangeKind, r.AuthorID, r.AuthorName, r.RestoredFrom, r.CreatedAt))
	}
	return out, nil
}

func (h *Handlers) GetSemanticMetricVersion(ctx context.Context, request GetSemanticMetricVersionRequestObject) (GetSemanticMetricVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticMetric, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return GetSemanticMetricVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSemanticMetricVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	detail, err := contentVersionDetail(v.Version, v.ChangeKind, v.AuthorID, v.AuthorName, v.RestoredFrom, v.CreatedAt, v.Snapshot)
	if err != nil {
		return nil, err
	}
	return GetSemanticMetricVersion200JSONResponse(detail), nil
}

func (h *Handlers) RestoreSemanticMetricVersion(ctx context.Context, request RestoreSemanticMetricVersionRequestObject) (RestoreSemanticMetricVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticMetric, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RestoreSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceEditor); err != nil {
		if unauthorized(err) {
			return RestoreSemanticMetricVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return RestoreSemanticMetricVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return RestoreSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	var spaceID *pgtype.UUID
	if request.Body != nil && request.Body.SpaceId != nil {
		dest := pgtype.UUID{Bytes: *request.Body.SpaceId, Valid: true}
		if se, err := h.contentSpace(ctx, ws, dest, authz.SpaceEditor); err != nil {
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return RestoreSemanticMetricVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit the destination space"}}, nil
			}
			return RestoreSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("destination space not found")}, nil
		}
		spaceID = &dest
	}

	// A metric restore re-runs #33's validation; failure is a 422 with nothing
	// written.
	snap, err := content.ParseMetricSnapshot(v.Snapshot)
	if err != nil {
		return RestoreSemanticMetricVersion422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	def, _, err := parseDefinitionRaw(snap.Definition)
	if err != nil {
		return RestoreSemanticMetricVersion422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	catalogID := pgtype.UUID{Bytes: snap.CatalogID, Valid: true}
	if err := h.validateSemanticMetric(ctx, ws, catalogID, pgUUIDFromPtr(snap.BindingID), actor, def, snap.Namespace, snap.Table, snap.Slug); err != nil {
		if te, ok := asTargetError(err); ok {
			return RestoreSemanticMetricVersion422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}

	var newSlug *string
	if request.Body != nil {
		newSlug = request.Body.NewSlug
	}
	m, err := h.contentService().RestoreSemanticMetric(ctx, actor, ws, id, int32(request.Version), newSlug, spaceID)
	switch {
	case errors.Is(err, content.ErrNameConflict):
		return RestoreSemanticMetricVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "name_conflict", Message: "another metric already uses this slug"}}, nil
	case errors.Is(err, content.ErrSpaceMissing):
		return RestoreSemanticMetricVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "space_missing", Message: "the version's space no longer exists; pass space_id"}}, nil
	case errors.Is(err, content.ErrNotFound):
		return RestoreSemanticMetricVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	case err != nil:
		return nil, err
	}
	return RestoreSemanticMetricVersion200JSONResponse(dbSemanticMetricToPublic(m, h.semanticMetricVersion(ctx, ws, m.ID))), nil
}

// ── trash ───────────────────────────────────────────────────────────────────────

func (h *Handlers) ListTrash(ctx context.Context, request ListTrashRequestObject) (ListTrashResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); err != nil {
		return ListTrash404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	lim, off := versionPage(request.Params.Limit, request.Params.Offset)
	var entityType *string
	if request.Params.EntityType != nil {
		t := string(*request.Params.EntityType)
		entityType = &t
	}
	rows, err := h.queries.ListDeletedContent(ctx, db.ListDeletedContentParams{
		WorkspaceID: ws, EntityType: entityType, Lim: lim, Off: off,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListTrash200JSONResponse, 0, len(rows))
	for _, r := range rows {
		item := TrashItem{
			EntityType:     TrashItemEntityType(r.EntityType),
			EntityId:       openapi_types.UUID(r.EntityID.Bytes),
			DeletedAt:      pgTimestampToTime(r.CreatedAt),
			Version:        int(r.Version),
			RestoreVersion: int(r.Version),
		}
		if r.SpaceID.Valid {
			id := openapi_types.UUID(r.SpaceID.Bytes)
			item.SpaceId = &id
			if r.SpaceName != nil {
				item.Space = &ContentSearchSpace{Id: id, Name: *r.SpaceName}
			}
		}
		switch r.EntityType {
		case content.EntitySavedQuery:
			s, err := content.ParseSavedQuerySnapshot(r.Snapshot)
			if err != nil {
				continue
			}
			item.Slug, item.Name = s.Slug, &s.Name
		case content.EntitySemanticMetric:
			s, err := content.ParseMetricSnapshot(r.Snapshot)
			if err != nil {
				continue
			}
			item.Slug, item.Name = s.Slug, &s.Label
		default:
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// ── converters ──────────────────────────────────────────────────────────────────

func contentVersionToPublic(version int32, kind string, authorID pgtype.UUID, authorName *string, restoredFrom *int32, createdAt pgtype.Timestamptz) ContentVersion {
	out := ContentVersion{
		Version:    int(version),
		ChangeKind: ContentVersionChangeKind(kind),
		CreatedAt:  pgTimestampToTime(createdAt),
	}
	if restoredFrom != nil {
		v := int(*restoredFrom)
		out.RestoredFrom = &v
	}
	if authorID.Valid {
		out.Author = &ContentAuthor{Id: openapi_types.UUID(authorID.Bytes), Name: authorName}
	}
	return out
}

func contentVersionDetail(version int32, kind string, authorID pgtype.UUID, authorName *string, restoredFrom *int32, createdAt pgtype.Timestamptz, snapshot []byte) (ContentVersionDetail, error) {
	var snap map[string]interface{}
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return ContentVersionDetail{}, err
	}
	out := ContentVersionDetail{
		Version:    int(version),
		ChangeKind: ContentVersionDetailChangeKind(kind),
		CreatedAt:  pgTimestampToTime(createdAt),
		Snapshot:   snap,
	}
	if restoredFrom != nil {
		v := int(*restoredFrom)
		out.RestoredFrom = &v
	}
	if authorID.Valid {
		out.Author = &ContentAuthor{Id: openapi_types.UUID(authorID.Bytes), Name: authorName}
	}
	return out, nil
}
