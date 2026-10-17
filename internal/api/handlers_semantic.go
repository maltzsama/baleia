package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/auth"
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
		return ListSemanticDocs200JSONResponse{dbSemanticDocToPublic(doc)}, nil
	}

	docs, err := h.queries.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: ws, CatalogID: catalogID, Namespace: request.Params.Ns,
		TableName: request.Params.Table, ColumnName: nil,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticDocs200JSONResponse, 0, len(docs))
	for _, d := range docs {
		out = append(out, dbSemanticDocToPublic(d))
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
	tags := []string{}
	if request.Body.Tags != nil {
		tags = *request.Body.Tags
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
	})
	if err != nil {
		return nil, err
	}
	return UpsertSemanticDoc200JSONResponse(dbSemanticDocToPublic(doc)), nil
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
	rows, err := h.queries.SearchSemanticDocs(ctx, db.SearchSemanticDocsParams{
		Q: p.Q, WorkspaceID: ws, Tag: tag, Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make(SearchSemanticDocs200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbSemanticSearchRowToPublic(r))
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
	metrics, err := h.queries.ListSemanticMetrics(ctx, ws)
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticMetrics200JSONResponse, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, dbSemanticMetricToPublic(m))
	}
	return out, nil
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
	return GetSemanticMetric200JSONResponse(dbSemanticMetricToPublic(metric)), nil
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

	metric, err := h.queries.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
		WorkspaceID:   ws,
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
	return CreateSemanticMetric201JSONResponse(dbSemanticMetricToPublic(metric)), nil
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

	body := request.Body
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

	updated, err := h.queries.UpdateSemanticMetric(ctx, db.UpdateSemanticMetricParams{
		WorkspaceID:   ws,
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
	})
	if err != nil {
		if isUniqueViolation(err) {
			return UpdateSemanticMetric409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "a metric with this slug already exists in this workspace"}}, nil
		}
		return nil, err
	}
	return UpdateSemanticMetric200JSONResponse(dbSemanticMetricToPublic(updated)), nil
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
	deleted, err := h.queries.DeleteSemanticMetric(ctx, db.DeleteSemanticMetricParams{
		WorkspaceID: ws, ID: pgtype.UUID{Bytes: request.MetricId, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteSemanticMetric404JSONResponse{NotFoundJSONResponse: metadataNotFound("metric not found")}, nil
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
	return out
}

func dbSemanticMetricToPublic(m db.SemanticMetric) SemanticMetric {
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
		UpdatedAt:     pgTimestampToTime(m.UpdatedAt),
	}
	var def map[string]interface{}
	if err := json.Unmarshal(m.Definition, &def); err == nil {
		out.Definition = def
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
