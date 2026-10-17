package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
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
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Semantic layer reads and writes (#33). Docs are keyed by the natural key
// (catalog + namespace + table [+ column]) and scoped to the workspace; metrics
// are structured definitions rendered per dialect and validated against a
// real, read-only engine session with the caller's identity. #40 moves metrics
// into spaces and #34 versions them.

const maxDocDescription = 20000

var semanticTagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// semanticCatalog checks the workspace may use the catalog. notFound covers a
// foreign or missing workspace/catalog; err is anything else.
func (h *Handlers) semanticCatalog(ctx context.Context, ws pgtype.UUID, catalogID openapi_types.UUID) (notFound bool, err error) {
	_, ok, err := h.grantedCatalog(ctx, ws, catalogID)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// normalizeColumn treats an empty query value as "not set" (table-level doc).
func normalizeColumn(column *string) *string {
	if column == nil {
		return nil
	}
	if strings.TrimSpace(*column) == "" {
		return nil
	}
	return column
}

// ── Docs ────────────────────────────────────────────────────────────────────────

// ListSemanticDocs — GET /workspaces/{id}/semantic/docs
func (h *Handlers) ListSemanticDocs(ctx context.Context, request ListSemanticDocsRequestObject) (ListSemanticDocsResponseObject, error) {
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

	catalogID := pgtype.UUID{Bytes: request.Params.CatalogId, Valid: true}
	column := normalizeColumn(request.Params.Column)
	if column != nil {
		doc, err := h.queries.GetSemanticDoc(ctx, db.GetSemanticDocParams{
			WorkspaceID: ws, CatalogID: catalogID, Namespace: request.Params.Ns,
			TableName: request.Params.Table, ColumnName: column,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ListSemanticDocs404JSONResponse{NotFoundJSONResponse: metadataNotFound("doc not found")}, nil
		}
		if err != nil {
			return nil, err
		}
		out := dbSemanticDocToPublic(doc)
		acts := h.docActions(ctx, ws)
		out.AllowedActions = &acts
		return ListSemanticDocs200JSONResponse{out}, nil
	}

	docs, err := h.queries.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: ws, CatalogID: catalogID, Namespace: request.Params.Ns,
		TableName: request.Params.Table, ColumnName: nil,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticDocs200JSONResponse, 0, len(docs))
	acts := h.docActions(ctx, ws)
	for _, d := range docs {
		doc := dbSemanticDocToPublic(d)
		doc.AllowedActions = &acts
		out = append(out, doc)
	}
	return out, nil
}

// UpsertSemanticDoc — PUT /workspaces/{id}/semantic/docs
func (h *Handlers) UpsertSemanticDoc(ctx context.Context, request UpsertSemanticDocRequestObject) (UpsertSemanticDocResponseObject, error) {
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
	if utf8.RuneCountInString(request.Body.Description) > maxDocDescription {
		return UpsertSemanticDoc422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "description must be at most 20000 characters")}, nil
	}

	updatedBy, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var owner pgtype.UUID
	if request.Body.OwnerUserId != nil {
		owner = pgtype.UUID{Bytes: *request.Body.OwnerUserId, Valid: true}
	}
	tags, tagErr := normalizeSemanticTags(request.Body.Tags)
	if tagErr != "" {
		return UpsertSemanticDoc422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", tagErr)}, nil
	}

	params := db.GetSemanticDocParams{
		WorkspaceID: ws,
		CatalogID:   pgtype.UUID{Bytes: request.Params.CatalogId, Valid: true},
		Namespace:   request.Params.Ns,
		TableName:   request.Params.Table,
		ColumnName:  normalizeColumn(request.Params.Column),
	}
	current, cerr := h.queries.GetSemanticDoc(ctx, params)
	exists := cerr == nil
	if cerr != nil && !errors.Is(cerr, pgx.ErrNoRows) {
		return nil, cerr
	}
	if exists && request.Body.ExpectedUpdatedAt != nil && current.UpdatedAt.Valid &&
		!request.Body.ExpectedUpdatedAt.Equal(current.UpdatedAt.Time) {
		by := openapi_types.UUID(current.UpdatedBy.Bytes)
		at := current.UpdatedAt.Time.Format(time.RFC3339Nano)
		fields := map[string]interface{}{"updated_by": by, "updated_at": at}
		return UpsertSemanticDoc409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: "version_conflict", Message: "the doc changed since you loaded it", Params: &fields,
		}}, nil
	}

	canCertify := h.canCertifyDocs(ctx, ws)
	certified := exists && current.Certified
	if request.Body.Certified != nil {
		if *request.Body.Certified && !canCertify {
			return UpsertSemanticDoc403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "only a workspace owner or admin may certify docs"}}, nil
		}
		certified = *request.Body.Certified
	} else if exists && current.Certified && current.Description != request.Body.Description && !canCertify {
		certified = false
	}

	doc, err := h.queries.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: ws,
		CatalogID:   pgtype.UUID{Bytes: request.Params.CatalogId, Valid: true},
		Namespace:   request.Params.Ns,
		TableName:   request.Params.Table,
		ColumnName:  normalizeColumn(request.Params.Column),
		Description: request.Body.Description,
		Tags:        tags,
		OwnerUserID: owner,
		UpdatedBy:   updatedBy,
		Certified:   certified,
	})
	if err != nil {
		return nil, err
	}
	if certified != (exists && current.Certified) {
		action := audit.SemanticDocCertify
		if !certified {
			action = audit.SemanticDocUncertify
		}
		h.audited(ctx, audit.Event{Action: action, ResourceType: "semantic_doc", ResourceID: idString(doc.ID), WorkspaceID: idString(ws)}.Allowed())
	}
	out := dbSemanticDocToPublic(doc)
	acts := h.docActions(ctx, ws)
	out.AllowedActions = &acts
	return UpsertSemanticDoc200JSONResponse(out), nil
}

// normalizeSemanticTags lowercases, dedupes and validates the tag list (#116).
func normalizeSemanticTags(tags *[]string) ([]string, string) {
	if tags == nil {
		return []string{}, ""
	}
	if len(*tags) > 20 {
		return nil, "at most 20 tags are allowed"
	}
	out := make([]string, 0, len(*tags))
	seen := map[string]bool{}
	for _, tag := range *tags {
		t := strings.ToLower(strings.TrimSpace(tag))
		if !semanticTagRe.MatchString(t) {
			return nil, "tags must match " + semanticTagRe.String()
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, ""
}

// canCertifyDocs is true for a workspace owner or a global admin (#116).
func (h *Handlers) canCertifyDocs(ctx context.Context, ws pgtype.UUID) bool {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return false
	}
	_, err = h.authz().Workspace(ctx, p, ws, authz.ActAdmin)
	return err == nil
}

func (h *Handlers) docActions(ctx context.Context, ws pgtype.UUID) []string {
	acts := []string{"view", "update"}
	if h.canCertifyDocs(ctx, ws) {
		acts = append(acts, "certify")
	}
	return acts
}

// DeleteSemanticDoc — DELETE /workspaces/{id}/semantic/docs
func (h *Handlers) DeleteSemanticDoc(ctx context.Context, request DeleteSemanticDocRequestObject) (DeleteSemanticDocResponseObject, error) {
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

	deleted, err := h.queries.DeleteSemanticDoc(ctx, db.DeleteSemanticDocParams{
		WorkspaceID: ws,
		CatalogID:   pgtype.UUID{Bytes: request.Params.CatalogId, Valid: true},
		Namespace:   request.Params.Ns,
		TableName:   request.Params.Table,
		ColumnName:  normalizeColumn(request.Params.Column),
	})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteSemanticDoc404JSONResponse{NotFoundJSONResponse: metadataNotFound("doc not found")}, nil
	}
	return DeleteSemanticDoc204Response{}, nil
}

// SearchSemanticDocs — GET /workspaces/{id}/semantic/docs/search
func (h *Handlers) SearchSemanticDocs(ctx context.Context, request SearchSemanticDocsRequestObject) (SearchSemanticDocsResponseObject, error) {
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
		return SearchSemanticDocs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "q must be 2 to 200 characters")}, nil
	}
	limit := defaultSearch
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxSearch {
			return SearchSemanticDocs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "limit must be between 1 and 200")}, nil
		}
		limit = *p.Limit
	}
	tag := p.Tag
	if tag != nil && strings.TrimSpace(*tag) == "" {
		tag = nil
	}
	params := db.SearchSemanticDocsParams{
		Q: p.Q, WorkspaceID: ws, Tag: tag, Certified: p.Certified, Lim: int32(limit),
	}
	if p.OwnerId != nil {
		params.OwnerID = pgtype.UUID{Bytes: *p.OwnerId, Valid: true}
	}
	rows, err := h.queries.SearchSemanticDocs(ctx, params)
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
func (h *Handlers) ListSemanticMetrics(ctx context.Context, request ListSemanticMetricsRequestObject) (ListSemanticMetricsResponseObject, error) {
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
	metrics, err := h.queries.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{
		WorkspaceID: ws, UserID: userID,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticMetrics200JSONResponse, 0, len(metrics))
	versions, err := h.queries.ListSemanticMetricVersions(ctx, ws)
	if err != nil {
		return nil, err
	}
	versionByID := make(map[string]int32, len(versions))
	for _, v := range versions {
		versionByID[uuidStr(v.EntityID)] = v.Version
	}
	q := ""
	if request.Params.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*request.Params.Q))
	}
	for _, m := range metrics {
		if request.Params.SpaceId != nil && m.SpaceID.Bytes != [16]byte(*request.Params.SpaceId) {
			continue
		}
		if request.Params.CatalogId != nil && m.CatalogID.Bytes != [16]byte(*request.Params.CatalogId) {
			continue
		}
		if request.Params.Certified != nil && m.Certified != *request.Params.Certified {
			continue
		}
		if request.Params.OwnerId != nil && m.OwnerUserID.Bytes != [16]byte(*request.Params.OwnerId) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Label), q) && !strings.Contains(strings.ToLower(m.Slug), q) && !strings.Contains(strings.ToLower(m.Description), q) {
			continue
		}
		metric := dbSemanticMetricToPublic(m, versionByID[uuidStr(m.ID)])
		acts := h.metricActions(ctx, ws, m.SpaceID)
		metric.AllowedActions = &acts
		out = append(out, metric)
	}
	return out, nil
}

// metricActions is what the caller may do with a metric (#115): view always,
// edit/delete for space editors, certify for space managers or admins.
func (h *Handlers) metricActions(ctx context.Context, ws, spaceID pgtype.UUID) []string {
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
func (h *Handlers) GetSemanticMetric(ctx context.Context, request GetSemanticMetricRequestObject) (GetSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metric, err := h.queries.GetSemanticMetric(ctx, db.GetSemanticMetricParams{
		WorkspaceID: ws, ID: pgtype.UUID{Bytes: request.MetricId, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, metric.SpaceID, authz.SpaceViewer)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	out := dbSemanticMetricToPublic(metric, h.semanticMetricVersion(ctx, ws, metric.ID))
	acts := h.metricActions(ctx, ws, metric.SpaceID)
	out.AllowedActions = &acts
	return GetSemanticMetric200JSONResponse(out), nil
}

// CreateSemanticMetric — POST /workspaces/{id}/semantic/metrics
func (h *Handlers) CreateSemanticMetric(ctx context.Context, request CreateSemanticMetricRequestObject) (CreateSemanticMetricResponseObject, error) {
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

	def, raw, err := parseDefinition(request.Body.Definition)
	if err != nil {
		return CreateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	if msg, ok := validateMetricFields(request.Body.Slug, request.Body.Label, request.Body.Table); !ok {
		return CreateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", msg)}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	bindingID := emptyUUID(request.Body.BindingId)
	ownerID := emptyUUID(request.Body.OwnerUserId)
	description, certified := "", false
	if request.Body.Description != nil {
		description = *request.Body.Description
	}
	if request.Body.Certified != nil {
		certified = *request.Body.Certified
	}

	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	if err := h.validateSemanticMetric(ctx, ws, catalogID, bindingID, userID, def, request.Body.Namespace, request.Body.Table, request.Body.Slug); err != nil {
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
				return CreateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
			}
			return nil, err
		}
		if se != nil {
			if se.status == 403 {
				return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	} else {
		space, err = h.queries.GetDefaultSpace(ctx, ws)
		if err != nil {
			return nil, err
		}
		if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceEditor); err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return CreateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
			}
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	}
	isManager := false
	if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	if certified && !isManager {
		return CreateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "only a space manager may certify a metric"}}, nil
	}
	metric, err := h.contentService().CreateSemanticMetric(ctx, userID, db.CreateSemanticMetricParams{
		WorkspaceID:   ws,
		SpaceID:       space.ID,
		Slug:          request.Body.Slug,
		Label:         request.Body.Label,
		Description:   description,
		Certified:     certified,
		CatalogID:     catalogID,
		Namespace:     request.Body.Namespace,
		TableName:     request.Body.Table,
		BindingID:     bindingID,
		Definition:    raw,
		FormatVersion: semantic.FormatVersion,
		OwnerUserID:   ownerID,
		UpdatedBy:     userID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CreateSemanticMetric409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "a metric with this slug already exists in this workspace"}}, nil
		}
		return nil, err
	}
	if certified {
		if recertified, cerr := h.queries.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
			WorkspaceID: ws, ID: metric.ID, Certified: true, Actor: userID,
		}); cerr == nil {
			metric = recertified
		}
	}
	out := dbSemanticMetricToPublic(metric, 1)
	acts := h.metricActions(ctx, ws, metric.SpaceID)
	out.AllowedActions = &acts
	return CreateSemanticMetric201JSONResponse(out), nil
}

// UpdateSemanticMetric — PATCH /workspaces/{id}/semantic/metrics/{metric_id}
func (h *Handlers) UpdateSemanticMetric(ctx context.Context, request UpdateSemanticMetricRequestObject) (UpdateSemanticMetricResponseObject, error) {
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
	current, err := h.queries.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: ws, ID: metricID})
	if errors.Is(err, pgx.ErrNoRows) {
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return UpdateSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return UpdateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return UpdateSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}

	isManager := false
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	body := request.Body
	if body.Certified != nil && *body.Certified != current.Certified && !isManager {
		return UpdateSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "only a space manager may certify a metric"}}, nil
	}
	if body.Certified != nil {
		// Handled after the update so certified_by/at stay consistent.
		_ = body.Certified
	}
	changesDefinition := body.Definition != nil || body.CatalogId != nil || body.Namespace != nil || body.Table != nil
	slug, label, tableName := current.Slug, current.Label, current.TableName
	namespace := current.Namespace
	description, certified := current.Description, current.Certified
	catalogID := current.CatalogID
	bindingID := current.BindingID
	ownerID := current.OwnerUserID
	raw := current.Definition
	if body.Slug != nil {
		slug = *body.Slug
	}
	if body.Label != nil {
		label = *body.Label
	}
	if body.Description != nil {
		description = *body.Description
	}
	if body.Certified != nil {
		certified = *body.Certified
	} else if changesDefinition && current.Certified && !isManager {
		certified = false
	}
	if body.CatalogId != nil {
		catalogID = pgtype.UUID{Bytes: *body.CatalogId, Valid: true}
	}
	if body.Namespace != nil {
		namespace = *body.Namespace
	}
	if body.Table != nil {
		tableName = *body.Table
	}
	if body.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *body.BindingId, Valid: true}
	}
	if body.OwnerUserId != nil {
		ownerID = pgtype.UUID{Bytes: *body.OwnerUserId, Valid: true}
	}
	if body.Definition != nil {
		if _, b, err := parseDefinition(*body.Definition); err != nil {
			return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
		} else {
			raw = b
		}
	}
	def, _, err := parseDefinitionRaw(raw)
	if err != nil {
		return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	if msg, ok := validateMetricFields(slug, label, tableName); !ok {
		return UpdateSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", msg)}, nil
	}

	nf, err = h.semanticCatalog(ctx, ws, openapi_types.UUID(catalogID.Bytes))
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
	if err := h.validateSemanticMetric(ctx, ws, catalogID, bindingID, userID, def, namespace, tableName, slug); err != nil {
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
	certificationChanged := certified != current.Certified
	updated, _, err := h.contentService().UpdateSemanticMetric(ctx, userID, db.UpdateSemanticMetricParams{
		WorkspaceID:   ws,
		SpaceID:       current.SpaceID,
		ID:            metricID,
		Slug:          slug,
		Label:         label,
		Description:   description,
		Certified:     certified,
		CatalogID:     catalogID,
		Namespace:     namespace,
		TableName:     tableName,
		BindingID:     bindingID,
		Definition:    raw,
		FormatVersion: semantic.FormatVersion,
		OwnerUserID:   ownerID,
		UpdatedBy:     userID,
	}, expected)
	if err != nil {
		if isUniqueViolation(err) {
			return UpdateSemanticMetric409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "a metric with this slug already exists in this workspace"}}, nil
		}
		if c, ok := h.versionConflictResponse(ctx, err); ok {
			return UpdateSemanticMetric409JSONResponse{ConflictJSONResponse: c}, nil
		}
		return nil, err
	}
	if certificationChanged || (updated.Certified && !updated.CertifiedAt.Valid) {
		if recertified, cerr := h.queries.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
			WorkspaceID: ws, ID: metricID, Certified: updated.Certified, Actor: userID,
		}); cerr == nil {
			updated = recertified
		}
	}
	out := dbSemanticMetricToPublic(updated, h.semanticMetricVersion(ctx, ws, metricID))
	acts := h.metricActions(ctx, ws, updated.SpaceID)
	out.AllowedActions = &acts
	return UpdateSemanticMetric200JSONResponse(out), nil
}

// DeleteSemanticMetric — DELETE /workspaces/{id}/semantic/metrics/{metric_id}
func (h *Handlers) DeleteSemanticMetric(ctx context.Context, request DeleteSemanticMetricRequestObject) (DeleteSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metricID := pgtype.UUID{Bytes: request.MetricId, Valid: true}
	current, err := h.queries.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: ws, ID: metricID})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSemanticMetric401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return DeleteSemanticMetric403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "you may not edit this space"}}, nil
		}
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.contentService().DeleteSemanticMetric(ctx, actor, ws, metricID); err != nil {
		if errors.Is(err, content.ErrNotFound) {
			return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
		}
		return nil, err
	}
	return DeleteSemanticMetric204Response{}, nil
}

// GetSemanticMetricSql — GET /workspaces/{id}/semantic/metrics/{metric_id}/sql
func (h *Handlers) GetSemanticMetricSql(ctx context.Context, request GetSemanticMetricSqlRequestObject) (GetSemanticMetricSqlResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	metric, err := h.queries.GetSemanticMetric(ctx, db.GetSemanticMetricParams{
		WorkspaceID: ws, ID: pgtype.UUID{Bytes: request.MetricId, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
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
			return GetSemanticMetricSql401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return GetSemanticMetricSql403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticMetricSql404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
	}
	def, _, err := parseDefinitionRaw(metric.Definition)
	if err != nil {
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}

	bindingID := metric.BindingID
	if request.Params.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Params.BindingId, Valid: true}
	}
	opts := semantic.RenderOptions{From: request.Params.From, To: request.Params.To}
	if request.Params.Grain != nil {
		opts.Grain = dialect.TimeGrain(*request.Params.Grain)
		if len(def.TimeGrains) > 0 && !slices.Contains(def.TimeGrains, opts.Grain) {
			return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("grain_not_allowed", "grain is not one of the metric's time_grains")}, nil
		}
	}
	if request.Params.From != nil && request.Params.To != nil && !request.Params.From.Before(*request.Params.To) {
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "from must be before to")}, nil
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
		return GetSemanticMetricSql422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	return GetSemanticMetricSql200JSONResponse(SemanticMetricSQL{Sql: sql, Portable: def.CustomSQL == nil}), nil
}

// ── metric validation ───────────────────────────────────────────────────────────

// parseDefinition decodes the request's free-form definition and rejects
// unknown fields; it returns the normalized JSON stored in the database.
func parseDefinition(in map[string]interface{}) (semantic.SimpleMetricV1, []byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	return parseDefinitionRaw(raw)
}

func parseDefinitionRaw(raw []byte) (semantic.SimpleMetricV1, []byte, error) {
	def, err := semantic.ParseSimpleMetricV1(raw)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	return def, normalized, nil
}

func validateMetricFields(slug, label, table string) (string, bool) {
	if !slugRe.MatchString(slug) {
		return "slug must match ^[a-z][a-z0-9_]{0,62}$", false
	}
	if strings.TrimSpace(label) == "" {
		return "label is required", false
	}
	if strings.TrimSpace(table) == "" {
		return "table is required", false
	}
	return "", true
}

func emptyUUID(v *openapi_types.UUID) pgtype.UUID {
	if v == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *v, Valid: true}
}

// semanticTarget resolves the binding a metric is validated/rendered against:
// the explicit binding, else the metric's binding, else the catalog's only
// enabled binding.
func (h *Handlers) semanticTarget(ctx context.Context, ws pgtype.UUID, catalogID, bindingID pgtype.UUID) (queryengine.Target, queryengine.Engine, error) {
	workspace, err := h.queries.GetWorkspaceByID(ctx, ws)
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
		return queryengine.Target{}, nil, &targetError{notFound: true, code: "not_found", message: "binding not found"}
	}
	if h.engines == nil {
		return queryengine.Target{}, nil, &targetError{code: "engine_unavailable", message: "no engine is available"}
	}
	engine, err := h.engines.Get(ctx, target.EngineID)
	if err != nil {
		return queryengine.Target{}, nil, &targetError{code: "engine_unavailable", message: "the metric's engine is not available"}
	}
	return target, engine, nil
}

// validateSemanticMetric renders the metric for the target binding and probes
// it with a zero-row, read-only statement in the caller's identity.
func (h *Handlers) validateSemanticMetric(ctx context.Context, ws pgtype.UUID, catalogID, bindingID, userID pgtype.UUID, def semantic.SimpleMetricV1, namespace []string, table, slug string) error {
	target, engine, err := h.semanticTarget(ctx, ws, catalogID, bindingID)
	if err != nil {
		return err
	}
	if def.CustomSQL != nil {
		if engine.Capabilities().ReadOnly != queryengine.ReadOnlyEnforced && !h.connectionReadOnly(ctx, target.EngineID) {
			return &targetError{code: "custom_sql_requires_read_only", message: "custom_sql needs an engine that enforces read-only sessions or read-only credentials"}
		}
		if policy.ForbiddenInEditor(engine.Dialect(), *def.CustomSQL) {
			return &targetError{code: "custom_sql_forbidden", message: "custom_sql may not contain catalog DDL or multiple statements"}
		}
	}
	sql, err := semantic.Render(def, semantic.TableRef{Catalog: target.EngineCatalog, Schema: namespace, Table: table}, engine.Dialect(), semantic.RenderOptions{Alias: slug})
	if err != nil {
		return &targetError{code: "invalid_definition", message: err.Error()}
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
		return &targetError{code: string(queryengine.CodeOf(err)), message: "the engine rejected the metric definition"}
	}
	return nil
}

func (h *Handlers) connectionReadOnly(ctx context.Context, engineID pgtype.UUID) bool {
	eng, err := h.queries.GetEngineByID(ctx, engineID)
	if err != nil {
		return false
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, eng.ConnectionID)
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
func (h *Handlers) semanticMetricVersion(ctx context.Context, ws, id pgtype.UUID) int32 {
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticMetric, EntityID: id,
	})
	if err != nil {
		return 0
	}
	return v.Version
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
	var def map[string]interface{}
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
func (h *Handlers) RenderSemanticMetric(ctx context.Context, request RenderSemanticMetricRequestObject) (RenderSemanticMetricResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return RenderSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	def, _, err := parseDefinition(request.Body.Definition)
	if err != nil {
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
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
		return RenderSemanticMetric422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
	}
	return RenderSemanticMetric200JSONResponse{Sql: sql}, nil
}

// ValidateSemanticMetricDefinition — POST …/semantic/metrics/validate (#115).
func (h *Handlers) ValidateSemanticMetricDefinition(ctx context.Context, request ValidateSemanticMetricDefinitionRequestObject) (ValidateSemanticMetricDefinitionResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ValidateSemanticMetricDefinition404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return ValidateSemanticMetricDefinition422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	def, _, err := parseDefinition(request.Body.Definition)
	if err != nil {
		return ValidateSemanticMetricDefinition422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("invalid_definition", err.Error())}, nil
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
func (h *Handlers) ListSemanticTags(ctx context.Context, request ListSemanticTagsRequestObject) (ListSemanticTagsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticTags404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	q := ""
	if request.Params.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*request.Params.Q))
	}
	limit := 20
	if request.Params.Limit != nil && *request.Params.Limit >= 1 && *request.Params.Limit <= 100 {
		limit = *request.Params.Limit
	}
	rows, err := h.queries.ListSemanticTags(ctx, db.ListSemanticTagsParams{
		WorkspaceID: ws, Q: q, Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticTags200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, SemanticTag{Tag: r.Tag, Count: int(r.Count)})
	}
	return out, nil
}
