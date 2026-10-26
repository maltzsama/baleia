package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/metadata/typemap"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Metadata reads (#18). Every handler reads the stored snapshot (#15) and
// never calls an engine; every read is scoped to the workspace and to the
// catalogs granted to it, and anything else is a 404.

const (
	defaultSearch  = 50
	maxSearch      = 200
	minSearchQuery = 2
	maxSearchQuery = 200
)

// metadataWorkspace checks the caller is a member of the workspace. notFound
// covers a foreign or missing workspace; err is anything else.
func (h *core) metadataWorkspace(ctx context.Context, id openapi_types.UUID) (ws pgtype.UUID, notFound bool, err error) {
	err = requireWorkspaceAccess(ctx, h.access, id)
	if errors.Is(err, ErrWorkspaceNotFound) {
		return pgtype.UUID{}, true, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	return workspaceIDFromRequest(id), false, nil
}

func metadataNotFound(msg string) NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: msg}
}

func freshness(t pgtype.Timestamptz) MetadataFreshness {
	return MetadataFreshness{MetadataSyncedAt: timePtr(t)}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// ListMetadataCatalogs — GET /workspaces/{id}/metadata/catalogs
func (h *CatalogHandlers) ListMetadataCatalogs(ctx context.Context, request ListMetadataCatalogsRequestObject) (ListMetadataCatalogsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataCatalogs404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	rows, err := h.browse.Catalogs(ctx, ws)
	if err != nil {
		return nil, err
	}
	out := make(ListMetadataCatalogs200JSONResponse, 0, len(rows))
	for _, r := range rows {
		c := MetadataCatalog{
			Id:                 openapi_types.UUID(r.ID.Bytes),
			Name:               r.Name,
			Origin:             MetadataCatalogOrigin(r.Origin),
			ProviderKind:       r.ProviderKind,
			MetadataSyncedAt:   timePtr(r.MetadataSyncedAt),
			MetadataSyncStatus: MetadataCatalogMetadataSyncStatus(r.MetadataSyncStatus),
			Bindings:           []MetadataCatalogBinding{},
		}
		if err := json.Unmarshal(r.Bindings, &c.Bindings); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// grantedCatalog loads a catalog granted to the workspace; ok is false when
// the workspace may not see it.
func (h *core) grantedCatalog(ctx context.Context, ws pgtype.UUID, id openapi_types.UUID) (db.GetGrantedMetadataCatalogRow, bool, error) {
	c, err := h.browse.Catalog(ctx, ws, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, browse.ErrCatalogNotFound) {
		return c, false, nil
	}
	return c, err == nil, err
}

// ListMetadataNamespaces — GET /workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces
func (h *CatalogHandlers) ListMetadataNamespaces(ctx context.Context, request ListMetadataNamespacesRequestObject) (ListMetadataNamespacesResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataNamespaces404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	var parent []string
	if request.Params.Parent != nil {
		parent = *request.Params.Parent
	}
	cat, rows, err := h.browse.Namespaces(ctx, ws, pgtype.UUID{Bytes: request.CatalogId, Valid: true}, parent)
	if errors.Is(err, browse.ErrCatalogNotFound) {
		return ListMetadataNamespaces404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	out := ListMetadataNamespaces200JSONResponse{Freshness: freshness(cat.MetadataSyncedAt)}
	out.Items = make([]struct {
		Path []string `json:"path"`
	}, len(rows))
	for i, ns := range rows {
		out.Items[i].Path = ns
	}
	return out, nil
}

// ListMetadataTables — GET /workspaces/{id}/metadata/catalogs/{catalog_id}/tables
// Pages by name: the cursor is the last name of the previous page, encoded.
func (h *CatalogHandlers) ListMetadataTables(ctx context.Context, request ListMetadataTablesRequestObject) (ListMetadataTablesResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataTables404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	q := browse.TablesQuery{Limit: request.Params.Limit}
	if request.Params.Cursor != nil {
		q.Cursor = *request.Params.Cursor
	}
	if request.Params.Ns != nil {
		q.Namespace = *request.Params.Ns
	}
	page, err := h.browse.Tables(ctx, ws, pgtype.UUID{Bytes: request.CatalogId, Valid: true}, q)
	var invalid *browse.InvalidError
	switch {
	case errors.Is(err, browse.ErrCatalogNotFound):
		return ListMetadataTables404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	case errors.As(err, &invalid):
		return ListMetadataTables422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case err != nil:
		return nil, err
	}
	out := ListMetadataTables200JSONResponse{Freshness: freshness(page.Catalog.MetadataSyncedAt), Items: []MetadataTableSummary{}, NextCursor: page.NextCursor}
	for _, r := range page.Tables {
		out.Items = append(out.Items, MetadataTableSummary{
			Id: openapi_types.UUID(r.ID.Bytes), Name: r.Name, Kind: MetadataTableSummaryKind(r.Kind),
			Comment: r.Comment, LastChangedAt: timePtr(r.LastChangedAt),
		})
	}
	return out, nil
}

// GetMetadataTable — GET /workspaces/{id}/metadata/tables/{table_id}
// Types and the qualified name are rendered for one of the catalog's
// bindings: the given one, else its first enabled binding.
func (h *CatalogHandlers) GetMetadataTable(ctx context.Context, request GetMetadataTableRequestObject) (GetMetadataTableResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	var bindingID pgtype.UUID
	if request.Params.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Params.BindingId, Valid: true}
	}
	detail, err := h.browse.Table(ctx, ws, pgtype.UUID{Bytes: request.TableId, Valid: true}, bindingID)
	switch {
	case errors.Is(err, browse.ErrTableNotFound):
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("table not found")}, nil
	case errors.Is(err, browse.ErrBindingNotFound):
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("binding not found")}, nil
	case err != nil:
		return nil, err
	}
	t, cols := detail.Table, detail.Columns
	tableDoc, columnDocs := semanticDocsByColumn(detail.Docs)

	out := GetMetadataTable200JSONResponse{
		Freshness: freshness(t.MetadataSyncedAt),
		Id:        openapi_types.UUID(t.ID.Bytes), CatalogId: openapi_types.UUID(t.CatalogID.Bytes),
		Namespace: t.Namespace, Name: t.Name, Kind: MetadataTableDetailKind(t.Kind), Comment: t.Comment,
		Properties:    map[string]string{},
		LastChangedAt: timePtr(t.LastChangedAt), RemovedAt: timePtr(t.RemovedAt),
		Columns: make([]MetadataColumn, 0, len(cols)),
		Doc:     tableDoc,
	}
	decodeStored(t.Properties, &out.Properties, "metadata_tables.properties")
	var d dialect.Dialect
	if b := detail.Binding; b != nil {
		id := openapi_types.UUID(b.ID.Bytes)
		out.BindingId = &id
		if dd, ok := h.dialects.ForEngineKind(b.EngineKind); ok {
			d = dd
			if q, err := d.Qualify(b.EngineCatalogName, t.Namespace, t.Name); err == nil {
				out.QualifiedName = &q
				// The preview is rendered by the binding's dialect so the UI
				// never has to hard-code LIMIT/TOP/FETCH FIRST.
				preview := d.PreviewSQL(q, 100)
				out.PreviewSql = &preview
			}
		}
	}
	for _, c := range cols {
		logical := typemap.Logical(c.TypeSystem, c.NativeType)
		col := MetadataColumn{
			Ordinal: int(c.Ordinal), Name: c.Name, NativeType: c.NativeType, TypeSystem: c.TypeSystem,
			LogicalType: &logical, Nullable: c.Nullable, Comment: c.Comment,
		}
		if d, ok := columnDocs[c.Name]; ok {
			col.Doc = d
		}
		if d != nil {
			if shown, ok := typemap.Display(c.TypeSystem, d.Name(), c.NativeType); ok {
				col.DisplayType = &shown
			}
		}
		out.Columns = append(out.Columns, col)
	}
	return out, nil
}

// SearchMetadata — GET /workspaces/{id}/metadata/search
func (h *CatalogHandlers) SearchMetadata(ctx context.Context, request SearchMetadataRequestObject) (SearchMetadataResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return SearchMetadata404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	p := request.Params
	q := browse.SearchQuery{Text: p.Q, Limit: p.Limit}
	if p.Kind != nil {
		q.Kind = string(*p.Kind)
	}
	if p.CatalogId != nil {
		q.CatalogID = pgtype.UUID{Bytes: *p.CatalogId, Valid: true}
	}
	rows, err := h.browse.Search(ctx, ws, q)
	var invalid *browse.InvalidError
	if errors.As(err, &invalid) {
		return SearchMetadata422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(SearchMetadata200JSONResponse, 0, len(rows))
	for _, r := range rows {
		res := MetadataSearchResult{
			Kind: MetadataSearchResultKind(r.Kind), TableId: openapi_types.UUID(r.TableID.Bytes),
			CatalogId: openapi_types.UUID(r.CatalogID.Bytes), Namespace: r.Namespace, Table: r.TableName,
			Score: r.Score, MetadataSyncedAt: timePtr(r.MetadataSyncedAt),
		}
		if r.Kind == "column" {
			col, typ := r.ColumnName, r.NativeType
			res.Column, res.NativeType = &col, &typ
		}
		out = append(out, res)
	}
	return out, nil
}
