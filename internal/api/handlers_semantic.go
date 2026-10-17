package api

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Semantic layer reads and writes (#33). Docs are keyed by the natural key
// (catalog + namespace + table [+ column]) and scoped to the workspace; the
// catalog must be granted to it (#16). Metrics become content items in #40.

const maxDocDescription = 20000

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

// ── converters ────────────────────────────────────────────────────────────────

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

// nonNilStrings keeps JSON arrays [] instead of null.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
