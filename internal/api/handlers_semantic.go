package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/semantic/metrics"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Semantic layer reads and writes (#33). Docs are keyed by the natural key
// (catalog + namespace + table [+ column]) and scoped to the workspace; metrics
// are structured definitions rendered per dialect and validated against a
// real, read-only engine session with the caller's identity. #40 moves metrics
// into spaces and #34 versions them.

// semanticCatalog checks the workspace may use the catalog. notFound covers a
// foreign or missing workspace/catalog; err is anything else.
func (h *core) semanticCatalog(ctx context.Context, ws pgtype.UUID, catalogID openapi_types.UUID) (notFound bool, err error) {
	_, ok, err := h.grantedCatalog(ctx, ws, catalogID)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// ── Docs ────────────────────────────────────────────────────────────────────────

// ListSemanticDocs — GET /workspaces/{id}/semantic/docs
func (h *SemanticHandlers) ListSemanticDocs(ctx context.Context, request ListSemanticDocsRequestObject) (ListSemanticDocsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticDocs404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	nf, err = h.semanticCatalog(ctx, ws, request.Params.CatalogId)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticDocs404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}

	k := docKey(ws, request.Params.CatalogId, request.Params.Ns, request.Params.Table, request.Params.Column)
	acts := h.docActions(ctx, ws)
	if request.Params.Column != nil && strings.TrimSpace(*request.Params.Column) != "" {
		doc, err := h.docs.Get(ctx, k)
		if errors.Is(err, docs.ErrNotFound) {
			return ListSemanticDocs404JSONResponse{NotFoundJSONResponse: metadataNotFound("doc not found")}, nil
		}
		if err != nil {
			return nil, err
		}
		out := dbSemanticDocToPublic(doc)
		out.AllowedActions = &acts
		return ListSemanticDocs200JSONResponse{out}, nil
	}

	all, err := h.docs.ListForTable(ctx, k)
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticDocs200JSONResponse, 0, len(all))
	for _, d := range all {
		doc := dbSemanticDocToPublic(d)
		doc.AllowedActions = &acts
		out = append(out, doc)
	}
	return out, nil
}

// UpsertSemanticDoc — PUT /workspaces/{id}/semantic/docs
func (h *SemanticHandlers) UpsertSemanticDoc(ctx context.Context, request UpsertSemanticDocRequestObject) (UpsertSemanticDocResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return UpsertSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	nf, err = h.semanticCatalog(ctx, ws, request.Params.CatalogId)
	if err != nil {
		return nil, err
	}
	if nf {
		return UpsertSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic doc: missing body")
	}
	updatedBy, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	edit := docs.Edit{
		Description:       request.Body.Description,
		Tags:              request.Body.Tags,
		Certified:         request.Body.Certified,
		ExpectedUpdatedAt: request.Body.ExpectedUpdatedAt,
	}
	if request.Body.OwnerUserId != nil {
		edit.Owner = pgtype.UUID{Bytes: *request.Body.OwnerUserId, Valid: true}
	}
	k := docKey(ws, request.Params.CatalogId, request.Params.Ns, request.Params.Table, request.Params.Column)
	doc, certChanged, err := h.docs.Upsert(ctx, k, edit, updatedBy, h.canCertifyDocs(ctx, ws))
	var (
		invalidTags *docs.InvalidTagsError
		conflict    *docs.ConflictError
	)
	switch {
	case errors.Is(err, docs.ErrDescriptionTooLong):
		return UpsertSemanticDoc422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	case errors.As(err, &invalidTags):
		return UpsertSemanticDoc422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalidTags.Msg)}, nil
	case errors.As(err, &conflict):
		fields := map[string]any{"updated_by": openapi_types.UUID(conflict.UpdatedBy.Bytes), "updated_at": conflict.UpdatedAt.Format(time.RFC3339Nano)}
		return UpsertSemanticDoc409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeVersionConflict, Message: "the doc changed since you loaded it", Params: &fields,
		}}, nil
	case errors.Is(err, docs.ErrCertifyForbidden):
		return UpsertSemanticDoc403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	if certChanged {
		action := audit.SemanticDocCertify
		if !doc.Certified {
			action = audit.SemanticDocUncertify
		}
		h.audited(ctx, audit.Event{Action: action, ResourceType: "semantic_doc", ResourceID: idString(doc.ID), WorkspaceID: idString(ws)}.Allowed())
	}
	out := dbSemanticDocToPublic(doc)
	acts := h.docActions(ctx, ws)
	out.AllowedActions = &acts
	return UpsertSemanticDoc200JSONResponse(out), nil
}

// canCertifyDocs is true for a workspace owner or a global admin (#116).
func (h *core) canCertifyDocs(ctx context.Context, ws pgtype.UUID) bool {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return false
	}
	_, err = h.authz().Workspace(ctx, p, ws, authz.ActAdmin)
	return err == nil
}

func (h *core) docActions(ctx context.Context, ws pgtype.UUID) []string {
	acts := []string{"view", "update"}
	if h.canCertifyDocs(ctx, ws) {
		acts = append(acts, "certify")
	}
	return acts
}

// DeleteSemanticDoc — DELETE /workspaces/{id}/semantic/docs
func (h *SemanticHandlers) DeleteSemanticDoc(ctx context.Context, request DeleteSemanticDocRequestObject) (DeleteSemanticDocResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return DeleteSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	nf, err = h.semanticCatalog(ctx, ws, request.Params.CatalogId)
	if err != nil {
		return nil, err
	}
	if nf {
		return DeleteSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}

	err = h.docs.Delete(ctx, docKey(ws, request.Params.CatalogId, request.Params.Ns, request.Params.Table, request.Params.Column))
	if errors.Is(err, docs.ErrNotFound) {
		return DeleteSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("doc not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	return DeleteSemanticDoc204Response{}, nil
}

// SearchSemanticDocs — GET /workspaces/{id}/semantic/docs/search
func (h *SemanticHandlers) SearchSemanticDocs(ctx context.Context, request SearchSemanticDocsRequestObject) (SearchSemanticDocsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return SearchSemanticDocs404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	p := request.Params
	n := utf8.RuneCountInString(p.Q)
	if n < minSearchQuery || n > maxSearchQuery {
		return SearchSemanticDocs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "q must be 2 to 200 characters")}, nil
	}
	limit := defaultSearch
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxSearch {
			return SearchSemanticDocs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 200")}, nil
		}
		limit = *p.Limit
	}
	params := db.SearchSemanticDocsParams{
		Q: p.Q, WorkspaceID: ws, Tag: p.Tag, Certified: p.Certified, Lim: int32(limit),
	}
	if p.OwnerId != nil {
		params.OwnerID = pgtype.UUID{Bytes: *p.OwnerId, Valid: true}
	}
	rows, err := h.docs.Search(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make(SearchSemanticDocs200JSONResponse, 0, len(rows))
	acts := h.docActions(ctx, ws)
	for _, r := range rows {
		item := dbSemanticSearchRowToPublic(r)
		item.AllowedActions = &acts
		out = append(out, item)
	}
	return out, nil
}

// ── Metrics ─────────────────────────────────────────────────────────────────────

// ListSemanticMetrics — GET /workspaces/{id}/semantic/metrics
func (h *SemanticHandlers) ListSemanticMetrics(ctx context.Context, request ListSemanticMetricsRequestObject) (ListSemanticMetricsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticMetrics404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	limit := 0
	if request.Params.Limit != nil {
		if *request.Params.Limit < 1 || *request.Params.Limit > 200 {
			return ListSemanticMetrics422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 200")}, nil
		}
		limit = *request.Params.Limit
	}
	f := metrics.Filter{Certified: request.Params.Certified, Limit: limit}
	if request.Params.Q != nil {
		f.Q = *request.Params.Q
	}
	if request.Params.SpaceId != nil {
		f.SpaceID = &pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}
	if request.Params.CatalogId != nil {
		f.CatalogID = &pgtype.UUID{Bytes: *request.Params.CatalogId, Valid: true}
	}
	if request.Params.OwnerId != nil {
		f.OwnerID = &pgtype.UUID{Bytes: *request.Params.OwnerId, Valid: true}
	}
	items, err := h.metricDefs.List(ctx, ws, userID, f)
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticMetrics200JSONResponse, 0, len(items))
	for _, it := range items {
		metric := dbSemanticMetricToPublic(it.Metric, it.Version)
		acts := h.metricActions(ctx, ws, it.Metric.SpaceID)
		metric.AllowedActions = &acts
		out = append(out, metric)
	}
	return out, nil
}

// metricActions is what the caller may do with a metric (#115): view always,
// edit/delete for space editors, certify for space managers or admins.
func (h *core) metricActions(ctx context.Context, ws, spaceID pgtype.UUID) []string {
	acts := []string{"view"}
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceEditor); err == nil && se == nil {
		acts = append(acts, "update", "delete")
	}
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceManager); err == nil && se == nil {
		acts = append(acts, "certify")
	}
	return acts
}

// GetSemanticMetric — GET /workspaces/{id}/semantic/metrics/{metric_id}
func (h *SemanticHandlers) GetSemanticMetric(ctx context.Context, request GetSemanticMetricRequestObject) (GetSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metric, err := h.metricDefs.Get(ctx, ws, pgtype.UUID{Bytes: request.MetricId, Valid: true})
	if errors.Is(err, metrics.ErrNotFound) {
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, metric.SpaceID, authz.SpaceViewer)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	out := dbSemanticMetricToPublic(metric, h.semanticMetricVersion(ctx, ws, metric.ID))
	acts := h.metricActions(ctx, ws, metric.SpaceID)
	out.AllowedActions = &acts
	return GetSemanticMetric200JSONResponse(out), nil
}

// CreateSemanticMetric — POST /workspaces/{id}/semantic/metrics
func (h *SemanticHandlers) CreateSemanticMetric(ctx context.Context, request CreateSemanticMetricRequestObject) (CreateSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic metric: missing body")
	}
	nf, err = h.semanticCatalog(ctx, ws, request.Body.CatalogId)
	if err != nil {
		return nil, err
	}
	if nf {
		return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}

	f := metrics.Fields{
		Slug:      request.Body.Slug,
		Label:     request.Body.Label,
		CatalogID: pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true},
		Namespace: request.Body.Namespace,
		Table:     request.Body.Table,
		BindingID: emptyUUID(request.Body.BindingId),
		OwnerID:   emptyUUID(request.Body.OwnerUserId),
	}
	if request.Body.Description != nil {
		f.Description = *request.Body.Description
	}
	if request.Body.Certified != nil {
		f.Certified = *request.Body.Certified
	}
	prepared, err := metrics.Prepare(f, request.Body.Definition)
	var (
		invalidDef   *metrics.InvalidDefinitionError
		invalidField *metrics.InvalidFieldError
	)
	switch {
	case errors.As(err, &invalidDef):
		return CreateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, invalidDef.Error())}, nil
	case errors.As(err, &invalidField):
		return CreateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalidField.Msg)}, nil
	case err != nil:
		return nil, err
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}

	if err := h.validateSemanticMetric(ctx, ws, prepared.CatalogID, prepared.BindingID, userID, prepared.Def, prepared.Namespace, prepared.Table, prepared.Slug); err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound(te.message)}, nil
			}
			return CreateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}

	var space db.Space
	if request.Body.SpaceId != nil {
		var se *spaceErr
		space, se, err = h.spaceForWrite(ctx, ws, request.Body.SpaceId, authz.SpaceEditor)
		if err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return CreateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
			}
			return nil, err
		}
		if se != nil {
			if se.status == 403 {
				return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	} else {
		space, err = h.spaces.Default(ctx, ws)
		if err != nil {
			return nil, err
		}
		if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceEditor); err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return CreateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
			}
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	}
	isManager := false
	if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	metric, err := h.metricDefs.Create(ctx, userID, ws, space.ID, prepared, isManager)
	switch {
	case errors.Is(err, metrics.ErrCertifyForbidden):
		return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: err.Error()}}, nil
	case errors.Is(err, metrics.ErrSlugTaken):
		return CreateSemanticMetric409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	out := dbSemanticMetricToPublic(metric, 1)
	acts := h.metricActions(ctx, ws, metric.SpaceID)
	out.AllowedActions = &acts
	return CreateSemanticMetric201JSONResponse(out), nil
}

// UpdateSemanticMetric — PATCH /workspaces/{id}/semantic/metrics/{metric_id}
func (h *SemanticHandlers) UpdateSemanticMetric(ctx context.Context, request UpdateSemanticMetricRequestObject) (UpdateSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic metric: missing body")
	}
	metricID := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	current, err := h.metricDefs.Get(ctx, ws, metricID)
	if errors.Is(err, metrics.ErrNotFound) {
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return UpdateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return UpdateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}

	isManager := false
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	body := request.Body
	change := metrics.Change{
		Slug: body.Slug, Label: body.Label, Description: body.Description, Certified: body.Certified,
		Namespace: body.Namespace, Table: body.Table, Definition: body.Definition,
	}
	if body.CatalogId != nil {
		change.CatalogID = &pgtype.UUID{Bytes: *body.CatalogId, Valid: true}
	}
	if body.BindingId != nil {
		change.BindingID = &pgtype.UUID{Bytes: *body.BindingId, Valid: true}
	}
	if body.OwnerUserId != nil {
		change.OwnerID = &pgtype.UUID{Bytes: *body.OwnerUserId, Valid: true}
	}
	// A certification is applied after the update so certified_by/at stay
	// consistent.
	merged, err := metrics.Merge(current, change, isManager)
	var (
		invalidDef   *metrics.InvalidDefinitionError
		invalidField *metrics.InvalidFieldError
	)
	switch {
	case errors.Is(err, metrics.ErrCertifyForbidden):
		return UpdateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: err.Error()}}, nil
	case errors.As(err, &invalidDef):
		return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, invalidDef.Error())}, nil
	case errors.As(err, &invalidField):
		return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalidField.Msg)}, nil
	case err != nil:
		return nil, err
	}

	nf, err = h.semanticCatalog(ctx, ws, openapi_types.UUID(merged.CatalogID.Bytes))
	if err != nil {
		return nil, err
	}
	if nf {
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.validateSemanticMetric(ctx, ws, merged.CatalogID, merged.BindingID, userID, merged.Def, merged.Namespace, merged.Table, merged.Slug); err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound(te.message)}, nil
			}
			return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}

	var expected *int32
	if body.ExpectedVersion != nil {
		v := int32(*body.ExpectedVersion)
		expected = &v
	}
	updated, err := h.metricDefs.Update(ctx, userID, current, merged, expected)
	if errors.Is(err, metrics.ErrSlugTaken) {
		return UpdateSemanticMetric409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	}
	if err != nil {
		if c, ok := h.versionConflictResponse(ctx, err); ok {
			return UpdateSemanticMetric409JSONResponse{ConflictJSONResponse: c}, nil
		}
		return nil, err
	}
	out := dbSemanticMetricToPublic(updated, h.semanticMetricVersion(ctx, ws, metricID))
	acts := h.metricActions(ctx, ws, updated.SpaceID)
	out.AllowedActions = &acts
	return UpdateSemanticMetric200JSONResponse(out), nil
}

// DeleteSemanticMetric — DELETE /workspaces/{id}/semantic/metrics/{metric_id}
func (h *SemanticHandlers) DeleteSemanticMetric(ctx context.Context, request DeleteSemanticMetricRequestObject) (DeleteSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metricID := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	current, err := h.metricDefs.Get(ctx, ws, metricID)
	if errors.Is(err, metrics.ErrNotFound) {
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return DeleteSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.metricDefs.Delete(ctx, actor, ws, metricID); err != nil {
		if errors.Is(err, content.ErrNotFound) {
			return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
		}
		return nil, err
	}
	return DeleteSemanticMetric204Response{}, nil
}

// GetSemanticMetricSql — GET /workspaces/{id}/semantic/metrics/{metric_id}/sql
func (h *SemanticHandlers) GetSemanticMetricSql(ctx context.Context, request GetSemanticMetricSqlRequestObject) (GetSemanticMetricSqlResponseObject, error) { //nolint:revive // name fixed by the generated StrictServerInterface
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metric, err := h.metricDefs.Get(ctx, ws, pgtype.UUID{Bytes: request.MetricId, Valid: true})
	if errors.Is(err, metrics.ErrNotFound) {
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	nf, err = h.semanticCatalog(ctx, ws, openapi_types.UUID(metric.CatalogID.Bytes))
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	if se, err := h.contentSpace(ctx, ws, metric.SpaceID, authz.SpaceViewer); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSemanticMetricSql401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return GetSemanticMetricSql403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	def, _, err := metrics.ParseDefinitionRaw(metric.Definition)
	if err != nil {
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}

	bindingID := metric.BindingID
	if request.Params.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Params.BindingId, Valid: true}
	}
	opts := semantic.RenderOptions{From: request.Params.From, To: request.Params.To}
	if request.Params.Grain != nil {
		opts.Grain = dialect.TimeGrain(*request.Params.Grain)
		if len(def.TimeGrains) > 0 && !slices.Contains(def.TimeGrains, opts.Grain) {
			return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeGrainNotAllowed, "grain is not one of the metric's time_grains")}, nil
		}
	}
	if request.Params.From != nil && request.Params.To != nil && !request.Params.From.Before(*request.Params.To) {
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "from must be before to")}, nil
	}

	target, engine, err := h.semanticTarget(ctx, ws, metric.CatalogID, bindingID)
	if err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound(te.message)}, nil
			}
			return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}
	opts.Alias = metric.Slug
	sql, err := semantic.Render(def, semantic.TableRef{Catalog: target.EngineCatalog, Schema: metric.Namespace, Table: metric.TableName}, engine.Dialect(), opts)
	if err != nil {
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	return GetSemanticMetricSql200JSONResponse(SemanticMetricSQL{Sql: sql, Portable: def.CustomSQL == nil}), nil
}

// ── metric validation ───────────────────────────────────────────────────────────

func emptyUUID(v *openapi_types.UUID) pgtype.UUID {
	if v == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *v, Valid: true}
}

// semanticTarget resolves the binding a metric is validated/rendered against:
// the explicit binding, else the metric's binding, else the catalog's only
// enabled binding.
func (h *SemanticHandlers) semanticTarget(ctx context.Context, ws pgtype.UUID, catalogID, bindingID pgtype.UUID) (queryengine.Target, queryengine.Engine, error) {
	workspace, err := h.access.Workspace(ctx, ws)
	if err != nil {
		return queryengine.Target{}, nil, err
	}
	cid := openapi_types.UUID(catalogID.Bytes)
	req := &TargetRequest{CatalogId: &cid}
	if bindingID.Valid {
		bid := openapi_types.UUID(bindingID.Bytes)
		req.BindingId = &bid
	}
	target, err := h.ResolveTarget(ctx, workspace, req)
	if err != nil {
		return queryengine.Target{}, nil, err
	}
	if target.CatalogID.Bytes != catalogID.Bytes {
		return queryengine.Target{}, nil, &targetError{notFound: true, code: ErrorCodeNotFound, message: "binding not found"}
	}
	if h.engines == nil {
		return queryengine.Target{}, nil, &targetError{code: ErrorCodeEngineUnavailable, message: "no engine is available"}
	}
	engine, err := h.engines.Get(ctx, target.EngineID)
	if err != nil {
		return queryengine.Target{}, nil, &targetError{code: ErrorCodeEngineUnavailable, message: "the metric's engine is not available"}
	}
	return target, engine, nil
}

// validateSemanticMetric renders the metric for the target binding and probes
// it with a zero-row, read-only statement in the caller's identity.
func (h *SemanticHandlers) validateSemanticMetric(ctx context.Context, ws pgtype.UUID, catalogID, bindingID, userID pgtype.UUID, def semantic.SimpleMetricV1, namespace []string, table, slug string) error {
	target, engine, err := h.semanticTarget(ctx, ws, catalogID, bindingID)
	if err != nil {
		return err
	}
	if def.CustomSQL != nil {
		if engine.Capabilities().ReadOnly != queryengine.ReadOnlyEnforced && !h.connectionReadOnly(ctx, target.EngineID) {
			return &targetError{code: ErrorCodeCustomSqlRequiresReadOnly, message: "custom_sql needs an engine that enforces read-only sessions or read-only credentials"}
		}
		if policy.ForbiddenInEditor(engine.Dialect(), *def.CustomSQL) {
			return &targetError{code: ErrorCodeCustomSqlForbidden, message: "custom_sql may not contain catalog DDL or multiple statements"}
		}
	}
	sql, err := semantic.Render(def, semantic.TableRef{Catalog: target.EngineCatalog, Schema: namespace, Table: table}, engine.Dialect(), semantic.RenderOptions{Alias: slug})
	if err != nil {
		return &targetError{code: ErrorCodeInvalidDefinition, message: err.Error()}
	}
	trinoUser, err := h.trinoIdentityFor(ctx, userID)
	if err != nil {
		return err
	}
	spec := queryengine.SessionSpec{
		Catalog: target.EngineCatalog, Schema: namespace,
		Identity: queryengine.Identity{UserID: trinoUser},
		ReadOnly: true,
		Tags:     queryengine.Tags{Source: "semantic", RequestID: middleware.GetReqID(ctx)},
	}
	if _, err := queryengine.Collect(ctx, engine, spec, engine.Dialect().Limit(sql, 0), 1); err != nil {
		return &targetError{code: ErrorCode(queryengine.CodeOf(err)), message: "the engine rejected the metric definition"}
	}
	return nil
}

func (h *core) connectionReadOnly(ctx context.Context, engineID pgtype.UUID) bool {
	eng, err := h.topology.Engine(ctx, engineID)
	if err != nil {
		return false
	}
	conn, err := h.topology.Connection(ctx, eng.ConnectionID)
	if err != nil {
		return false
	}
	return conn.CredentialsReadOnly
}

// ── converters ──────────────────────────────────────────────────────────────────

func dbSemanticDocToPublic(d db.SemanticDoc) SemanticDoc {
	out := SemanticDoc{
		Id:          openapi_types.UUID(d.ID.Bytes),
		CatalogId:   openapi_types.UUID(d.CatalogID.Bytes),
		Namespace:   nonNilStrings(d.Namespace),
		Table:       d.TableName,
		Column:      d.ColumnName,
		Description: d.Description,
		Tags:        nonNilStrings(d.Tags),
		UpdatedBy:   openapi_types.UUID(d.UpdatedBy.Bytes),
		UpdatedAt:   pgTimestampToTime(d.UpdatedAt),
	}
	if d.OwnerUserID.Valid {
		owner := openapi_types.UUID(d.OwnerUserID.Bytes)
		out.OwnerUserId = &owner
	}
	out.Certified = &d.Certified
	if d.CertifiedBy.Valid {
		by := openapi_types.UUID(d.CertifiedBy.Bytes)
		out.CertifiedBy = &by
	}
	if d.CertifiedAt.Valid {
		at := pgTimestampToTime(d.CertifiedAt)
		out.CertifiedAt = &at
	}
	return out
}

func dbSemanticSearchRowToPublic(r db.SearchSemanticDocsRow) SemanticDocSearchResult {
	out := SemanticDocSearchResult{
		Id:          openapi_types.UUID(r.ID.Bytes),
		CatalogId:   openapi_types.UUID(r.CatalogID.Bytes),
		Namespace:   nonNilStrings(r.Namespace),
		Table:       r.TableName,
		Column:      r.ColumnName,
		Description: r.Description,
		Tags:        nonNilStrings(r.Tags),
		UpdatedBy:   openapi_types.UUID(r.UpdatedBy.Bytes),
		UpdatedAt:   pgTimestampToTime(r.UpdatedAt),
		Score:       r.Score,
	}
	if r.OwnerUserID.Valid {
		owner := openapi_types.UUID(r.OwnerUserID.Bytes)
		out.OwnerUserId = &owner
	}
	out.Certified = &r.Certified
	if r.CertifiedBy.Valid {
		by := openapi_types.UUID(r.CertifiedBy.Bytes)
		out.CertifiedBy = &by
	}
	if r.CertifiedAt.Valid {
		at := pgTimestampToTime(r.CertifiedAt)
		out.CertifiedAt = &at
	}
	return out
}

// semanticMetricVersion returns the latest content version, or 0 when none.
func (h *core) semanticMetricVersion(ctx context.Context, ws, id pgtype.UUID) int32 {
	return h.history.Latest(ctx, ws, content.EntitySemanticMetric, id)
}

func dbSemanticMetricToPublic(m db.SemanticMetric, version int32) SemanticMetric {
	out := SemanticMetric{
		Id:            openapi_types.UUID(m.ID.Bytes),
		WorkspaceId:   openapi_types.UUID(m.WorkspaceID.Bytes),
		Slug:          m.Slug,
		Label:         m.Label,
		Description:   &m.Description,
		Certified:     m.Certified,
		CatalogId:     openapi_types.UUID(m.CatalogID.Bytes),
		Namespace:     nonNilStrings(m.Namespace),
		Table:         m.TableName,
		FormatVersion: int(m.FormatVersion),
		Version:       int(version),
		UpdatedAt:     pgTimestampToTime(m.UpdatedAt),
	}
	var def map[string]any
	if err := json.Unmarshal(m.Definition, &def); err == nil {
		out.Definition = def
	}
	if m.SpaceID.Valid {
		v := openapi_types.UUID(m.SpaceID.Bytes)
		out.SpaceId = &v
	}
	if m.BindingID.Valid {
		v := openapi_types.UUID(m.BindingID.Bytes)
		out.BindingId = &v
	}
	if m.OwnerUserID.Valid {
		v := openapi_types.UUID(m.OwnerUserID.Bytes)
		out.OwnerUserId = &v
	}
	if m.UpdatedBy.Valid {
		v := openapi_types.UUID(m.UpdatedBy.Bytes)
		out.UpdatedBy = &v
	}
	if m.CertifiedBy.Valid {
		v := openapi_types.UUID(m.CertifiedBy.Bytes)
		out.CertifiedBy = &v
	}
	if m.CertifiedAt.Valid {
		v := pgTimestampToTime(m.CertifiedAt)
		out.CertifiedAt = &v
	}
	return out
}

// semanticDocsByColumn splits a table's docs into the table-level doc and a
// per-column map, for the metadata table response (#18).
func semanticDocsByColumn(docs []db.SemanticDoc) (*SemanticDoc, map[string]*SemanticDoc) {
	var table *SemanticDoc
	byColumn := make(map[string]*SemanticDoc, len(docs))
	for _, d := range docs {
		pub := dbSemanticDocToPublic(d)
		if d.ColumnName == nil {
			table = &pub
			continue
		}
		byColumn[*d.ColumnName] = &pub
	}
	return table, byColumn
}

// nonNilStrings keeps JSON arrays [] instead of null.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// RenderSemanticMetric — POST /workspaces/{id}/semantic/metrics/render (#115).
// Pure rendering: no engine call.
func (h *SemanticHandlers) RenderSemanticMetric(ctx context.Context, request RenderSemanticMetricRequestObject) (RenderSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return RenderSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	def, _, err := metrics.ParseDefinition(request.Body.Definition)
	if err != nil {
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	var bindingID pgtype.UUID
	if request.Body.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Body.BindingId, Valid: true}
	}
	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	target, engine, err := h.semanticTarget(ctx, ws, catalogID, bindingID)
	if err != nil {
		if te, ok := asTargetError(err); ok {
			return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}
	opts := semantic.RenderOptions{Alias: "metric"}
	if request.Body.Grain != nil && *request.Body.Grain != "" {
		opts.Grain = dialect.TimeGrain(*request.Body.Grain)
	}
	sql, err := semantic.Render(def, semantic.TableRef{Catalog: target.EngineCatalog, Schema: request.Body.Namespace, Table: request.Body.Table}, engine.Dialect(), opts)
	if err != nil {
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	return RenderSemanticMetric200JSONResponse{Sql: sql}, nil
}

// ValidateSemanticMetricDefinition — POST …/semantic/metrics/validate (#115).
func (h *SemanticHandlers) ValidateSemanticMetricDefinition(ctx context.Context, request ValidateSemanticMetricDefinitionRequestObject) (ValidateSemanticMetricDefinitionResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ValidateSemanticMetricDefinition404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return ValidateSemanticMetricDefinition422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	def, _, err := metrics.ParseDefinition(request.Body.Definition)
	if err != nil {
		return ValidateSemanticMetricDefinition422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var bindingID pgtype.UUID
	if request.Body.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Body.BindingId, Valid: true}
	}
	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	if err := h.validateSemanticMetric(ctx, ws, catalogID, bindingID, userID, def, request.Body.Namespace, request.Body.Table, "metric"); err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return ValidateSemanticMetricDefinition404JSONResponse{NotFoundJSONResponse: metadataNotFound(te.message)}, nil
			}
			return ValidateSemanticMetricDefinition422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}
	return ValidateSemanticMetricDefinition200JSONResponse{Ok: true}, nil
}

// ListSemanticTags — GET /workspaces/{id}/semantic/tags (#116).
func (h *SemanticHandlers) ListSemanticTags(ctx context.Context, request ListSemanticTagsRequestObject) (ListSemanticTagsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticTags404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	q := ""
	if request.Params.Q != nil {
		q = *request.Params.Q
	}
	limit := 20
	if request.Params.Limit != nil && *request.Params.Limit >= 1 && *request.Params.Limit <= 100 {
		limit = *request.Params.Limit
	}
	rows, err := h.docs.Tags(ctx, ws, q, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticTags200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, SemanticTag{Tag: r.Tag, Count: int(r.Count)})
	}
	return out, nil
}

// docKey names a documented table, or one of its columns.
func docKey(ws pgtype.UUID, catalogID openapi_types.UUID, ns []string, table string, column *string) docs.Key {
	return docs.Key{WorkspaceID: ws, CatalogID: pgtype.UUID{Bytes: catalogID, Valid: true}, Namespace: ns, Table: table, Column: column}
}
