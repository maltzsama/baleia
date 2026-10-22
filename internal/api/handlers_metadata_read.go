package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metadata/typemap"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Metadata reads (#18). Every handler reads the stored snapshot (#15) and
// never calls an engine; every read is scoped to the workspace and to the
// catalogs granted to it, and anything else is a 404.

const (
	defaultTablesPage = 100
	maxTablesPage     = 500
	defaultSearch     = 50
	maxSearch         = 200
	minSearchQuery    = 2
	maxSearchQuery    = 200
)

// metadataWorkspace checks the caller is a member of the workspace. notFound
// covers a foreign or missing workspace; err is anything else.
func (h *Handlers) metadataWorkspace(ctx context.Context, id openapi_types.UUID) (ws pgtype.UUID, notFound bool, err error) {
	err = requireWorkspaceAccess(ctx, h.queries, id)
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
func (h *Handlers) ListMetadataCatalogs(ctx context.Context, request ListMetadataCatalogsRequestObject) (ListMetadataCatalogsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataCatalogs404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	rows, err := h.queries.ListMetadataCatalogs(ctx, ws)
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
func (h *Handlers) grantedCatalog(ctx context.Context, ws pgtype.UUID, id openapi_types.UUID) (db.GetGrantedMetadataCatalogRow, bool, error) {
	c, err := h.queries.GetGrantedMetadataCatalog(ctx, db.GetGrantedMetadataCatalogParams{WorkspaceID: ws, CatalogID: pgtype.UUID{Bytes: id, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// ListMetadataNamespaces — GET /workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces
func (h *Handlers) ListMetadataNamespaces(ctx context.Context, request ListMetadataNamespacesRequestObject) (ListMetadataNamespacesResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataNamespaces404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	cat, ok, err := h.grantedCatalog(ctx, ws, request.CatalogId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListMetadataNamespaces404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	parent := []string{}
	if request.Params.Parent != nil {
		parent = *request.Params.Parent
	}
	rows, err := h.queries.ListChildNamespaces(ctx, db.ListChildNamespacesParams{CatalogID: cat.ID, Parent: parent})
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
func (h *Handlers) ListMetadataTables(ctx context.Context, request ListMetadataTablesRequestObject) (ListMetadataTablesResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListMetadataTables404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	cat, ok, err := h.grantedCatalog(ctx, ws, request.CatalogId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListMetadataTables404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	limit := defaultTablesPage
	if l := request.Params.Limit; l != nil {
		if *l < 1 || *l > maxTablesPage {
			return ListMetadataTables422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 500")}, nil
		}
		limit = *l
	}
	after := ""
	if c := request.Params.Cursor; c != nil && *c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(*c)
		if err != nil || !utf8.Valid(raw) {
			return ListMetadataTables422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "invalid cursor")}, nil
		}
		after = string(raw)
	}
	ns := []string{}
	if request.Params.Ns != nil {
		ns = *request.Params.Ns
	}
	rows, err := h.queries.ListMetadataTablesPage(ctx, db.ListMetadataTablesPageParams{CatalogID: cat.ID, Namespace: ns, After: after, Lim: int32(limit + 1)})
	if err != nil {
		return nil, err
	}
	out := ListMetadataTables200JSONResponse{Freshness: freshness(cat.MetadataSyncedAt), Items: []MetadataTableSummary{}}
	if len(rows) > limit {
		rows = rows[:limit]
		next := base64.RawURLEncoding.EncodeToString([]byte(rows[limit-1].Name))
		out.NextCursor = &next
	}
	for _, r := range rows {
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
func (h *Handlers) GetMetadataTable(ctx context.Context, request GetMetadataTableRequestObject) (GetMetadataTableResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	t, err := h.queries.GetGrantedMetadataTable(ctx, db.GetGrantedMetadataTableParams{WorkspaceID: ws, TableID: pgtype.UUID{Bytes: request.TableId, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("table not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	var bindingID pgtype.UUID
	if request.Params.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Params.BindingId, Valid: true}
	}
	b, err := h.queries.GetCatalogBindingForDisplay(ctx, db.GetCatalogBindingForDisplayParams{CatalogID: t.CatalogID, BindingID: bindingID})
	hasBinding := err == nil
	switch {
	case errors.Is(err, pgx.ErrNoRows) && bindingID.Valid:
		return GetMetadataTable404JSONResponse{NotFoundJSONResponse: metadataNotFound("binding not found")}, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	cols, err := h.queries.ListMetadataColumns(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	// Semantic docs (#33) are keyed by the natural key, not by the metadata
	// table id, so they survive a resync and are joined here at read time.
	docs, err := h.queries.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: ws, CatalogID: t.CatalogID, Namespace: t.Namespace, TableName: t.Name, ColumnName: nil,
	})
	if err != nil {
		return nil, err
	}
	tableDoc, columnDocs := semanticDocsByColumn(docs)

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
	if hasBinding {
		id := openapi_types.UUID(b.ID.Bytes)
		out.BindingId = &id
		if dd, ok := dialect.ForEngineKind(b.EngineKind); ok {
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
func (h *Handlers) SearchMetadata(ctx context.Context, request SearchMetadataRequestObject) (SearchMetadataResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return SearchMetadata404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	p := request.Params
	if n := utf8.RuneCountInString(p.Q); n < minSearchQuery || n > maxSearchQuery {
		return SearchMetadata422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "q must be 2 to 200 characters")}, nil
	}
	kind := "all"
	if p.Kind != nil {
		switch *p.Kind {
		case Table, Column, All:
			kind = string(*p.Kind)
		default:
			return SearchMetadata422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "kind must be table, column or all")}, nil
		}
	}
	limit := defaultSearch
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxSearch {
			return SearchMetadata422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 200")}, nil
		}
		limit = *p.Limit
	}
	var catalogID pgtype.UUID
	if p.CatalogId != nil {
		catalogID = pgtype.UUID{Bytes: *p.CatalogId, Valid: true}
	}
	rows, err := h.queries.SearchMetadata(ctx, db.SearchMetadataParams{WorkspaceID: ws, CatalogID: catalogID, Q: p.Q, Kind: kind, Lim: int32(limit)})
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
