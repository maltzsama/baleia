package api

import (
	"context"
	"encoding/json"

	cc "github.com/galileostudio/baleia/internal/catalog_connections"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListCatalogs(ctx context.Context, request ListCatalogsRequestObject) (ListCatalogsResponseObject, error) {
	client, err := h.catalogClient(ctx, request.CatalogConnectionId)
	if err != nil {
		return ListCatalogs404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	catalogs, err := client.ListCatalogs(ctx)
	if err != nil {
		return nil, err
	}

	result := make(ListCatalogs200JSONResponse, 0, len(catalogs))
	for _, c := range catalogs {
		name := c.Name
		ctype := c.Type
		result = append(result, struct {
			Name *string `json:"name,omitempty"`
			Type *string `json:"type,omitempty"`
		}{Name: &name, Type: &ctype})
	}
	return result, nil
}

func (h *Handlers) ListNamespaces(ctx context.Context, request ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	client, err := h.catalogClient(ctx, request.CatalogConnectionId)
	if err != nil {
		return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	namespaces, err := client.ListNamespaces(ctx, request.Catalog)
	if err != nil {
		return nil, err
	}

	return ListNamespaces200JSONResponse(namespaces), nil
}

func (h *Handlers) ListTables(ctx context.Context, request ListTablesRequestObject) (ListTablesResponseObject, error) {
	client, err := h.catalogClient(ctx, request.CatalogConnectionId)
	if err != nil {
		return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	tables, err := client.ListTables(ctx, request.Catalog, request.Namespace)
	if err != nil {
		return nil, err
	}

	result := make(ListTables200JSONResponse, 0, len(tables))
	for _, t := range tables {
		name := t.Name
		ns := t.Namespace
		result = append(result, struct {
			Name      *string   `json:"name,omitempty"`
			Namespace *[]string `json:"namespace,omitempty"`
		}{Name: &name, Namespace: &ns})
	}
	return result, nil
}

func (h *Handlers) ListColumns(ctx context.Context, request ListColumnsRequestObject) (ListColumnsResponseObject, error) {
	client, err := h.catalogClient(ctx, request.CatalogConnectionId)
	if err != nil {
		return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, request.Catalog, request.Namespace, request.Table)
	if err != nil {
		return nil, err
	}

	if len(meta.Schemas) == 0 {
		return ListColumns200JSONResponse{}, nil
	}

	schema := meta.Schemas[len(meta.Schemas)-1]
	result := make(ListColumns200JSONResponse, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		id := f.ID
		name := f.Name
		required := f.Required
		doc := f.Doc
		typeStr := ""
		if b, err := json.Marshal(f.Type); err == nil {
			typeStr = string(b)
		}
		result = append(result, struct {
			Doc      *string `json:"doc,omitempty"`
			Id       *int    `json:"id,omitempty"`
			Name     *string `json:"name,omitempty"`
			Required *bool   `json:"required,omitempty"`
			Type     *string `json:"type,omitempty"`
		}{Id: &id, Name: &name, Type: &typeStr, Required: &required, Doc: &doc})
	}
	return result, nil
}

func (h *Handlers) ListTableSnapshots(ctx context.Context, request ListTableSnapshotsRequestObject) (ListTableSnapshotsResponseObject, error) {
	client, err := h.catalogClient(ctx, request.CatalogConnectionId)
	if err != nil {
		return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, request.Catalog, request.Namespace, request.Table)
	if err != nil {
		return nil, err
	}

	result := make(ListTableSnapshots200JSONResponse, 0, len(meta.Snapshots))
	for _, s := range meta.Snapshots {
		snapshotID := s.SnapshotID
		timestampMs := s.TimestampMs
		operation := s.Operation
		manifestList := s.ManifestList
		result = append(result, struct {
			ManifestList *string `json:"manifest_list,omitempty"`
			Operation    *string `json:"operation,omitempty"`
			ParentId     *int64  `json:"parent_id,omitempty"`
			SnapshotId   *int64  `json:"snapshot_id,omitempty"`
			TimestampMs  *int64  `json:"timestamp_ms,omitempty"`
		}{
			SnapshotId:   &snapshotID,
			TimestampMs:  &timestampMs,
			Operation:    &operation,
			ManifestList: &manifestList,
			ParentId:     s.ParentSnapshotID,
		})
	}
	return result, nil
}

// ── helper ────────────────────────────────────────────────────────────────────

func (h *Handlers) catalogClient(ctx context.Context, id openapi_types.UUID) (cc.Client, error) {
	var uid pgtype.UUID
	uid.Bytes = id
	uid.Valid = true

	conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return nil, err
	}

	return cc.NewClient(conn.Type, conn.Url, conn.CredentialsEnc)
}
