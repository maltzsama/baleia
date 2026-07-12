package api

import (
	"context"
	"encoding/json"
	"time"
	"log/slog"
	
	catalog_connections "github.com/galileostudio/baleia/internal/catalog_connections"


	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	trino_client "github.com/galileostudio/baleia/internal/trino"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return ListCatalogConnections403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	conns, err := h.queries.ListCatalogConnections(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]CatalogConnection, 0, len(conns))
	for _, c := range conns {
		result = append(result, dbCatalogConnectionToPublic(c))
	}
	return ListCatalogConnections200JSONResponse(result), nil
}

func (h *Handlers) CreateCatalogConnection(ctx context.Context, request CreateCatalogConnectionRequestObject) (CreateCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return CreateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var credsEnc *string
	switch string(request.Body.Type) {
	case "polaris":
		if request.Body.PolarisCredentials != nil {
			b, err := json.Marshal(request.Body.PolarisCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "nessie":
		if request.Body.NessieCredentials != nil {
			b, err := json.Marshal(request.Body.NessieCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "glue":
		if request.Body.GlueCredentials != nil {
			b, err := json.Marshal(request.Body.GlueCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "hive":
		if request.Body.HiveCredentials != nil {
			b, err := json.Marshal(request.Body.HiveCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "iceberg":
		if request.Body.IcebergCredentials != nil {
			b, err := json.Marshal(request.Body.IcebergCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	}

	defaultReadonly := true
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}

	conn, err := h.queries.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		Name:            request.Body.Name,
		Type:            string(request.Body.Type),
		Url:             request.Body.Url,
		CredentialsEnc:  credsEnc,
		DefaultReadonly: defaultReadonly,
	})
	if err != nil {
		slog.Error("CreateCatalogConnection failed", "err", err)
		return nil, err
	}

	return CreateCatalogConnection201JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return GetCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	return GetCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return UpdateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	current, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return UpdateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	name := current.Name
	url := current.Url
	credsEnc := current.CredentialsEnc
	defaultReadonly := current.DefaultReadonly

	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Url != nil {
		url = *request.Body.Url
	}
	if request.Body.Credentials != nil {
		b, err := json.Marshal(request.Body.Credentials)
		if err != nil {
			return nil, err
		}
		s := string(b)
		credsEnc = &s
	}
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}

	conn, err := h.queries.UpdateCatalogConnection(ctx, db.UpdateCatalogConnectionParams{
		ID:              uid,
		Name:            name,
		Url:             url,
		CredentialsEnc:  credsEnc,
		DefaultReadonly: defaultReadonly,
	})
	if err != nil {
		return nil, err
	}

	return UpdateCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return DeleteCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return DeleteCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	if err := h.queries.DeleteCatalogConnection(ctx, uid); err != nil {
		return nil, err
	}

	return DeleteCatalogConnection204Response{}, nil
}

func (h *Handlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
    var uid pgtype.UUID
    uid.Bytes = request.Id
    uid.Valid = true

    conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
    if err != nil {
        return TestCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
    }

    start := time.Now()
    client, err := catalog_connections.NewClient(conn.Type, conn.Url, conn.CredentialsEnc)
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{
            Success:   false,
            LatencyMs: 0,
            Error:     &errMsg,
        }, nil
    }

    testErr := client.Ping(ctx)
    latencyMs := int(time.Since(start).Milliseconds())

    if testErr != nil {
        errMsg := testErr.Error()
        return TestCatalogConnection200JSONResponse{
            Success:   false,
            LatencyMs: latencyMs,
            Error:     &errMsg,
        }, nil
    }

    v := conn.Type
    return TestCatalogConnection200JSONResponse{
        Success:   true,
        LatencyMs: latencyMs,
        Version:   &v,
    }, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbCatalogConnectionToPublic(c db.CatalogConnection) CatalogConnection {
	id := openapi_types.UUID(c.ID.Bytes)
	return CatalogConnection{
		Id:              id,
		Name:            c.Name,
		Type:            CatalogConnectionType(c.Type),
		Url:             c.Url,
		DefaultReadonly: c.DefaultReadonly,
		CreatedAt:       pgTimestampToTime(c.CreatedAt),
		UpdatedAt:       func() *time.Time { t := pgTimestampToTime(c.UpdatedAt); return &t }(),
	}
}

func (h *Handlers) ListCatalogs(ctx context.Context, request ListCatalogsRequestObject) (ListCatalogsResponseObject, error) {
	var connID pgtype.UUID
	connID.Bytes = request.ConnectionId
	connID.Valid = true

	catalogs, err := h.queries.ListCatalogsByConnection(ctx, connID)
	if err != nil {
		return nil, err
	}

	result := make([]Catalog, 0, len(catalogs))
	for _, c := range catalogs {
		id := openapi_types.UUID(c.ID.Bytes)
		result = append(result, Catalog{
			Id:               &id,
			Name:             &c.Name,
			TrinoCatalogName: &c.TrinoCatalogName,
		})
	}
	return ListCatalogs200JSONResponse(result), nil
}

func (h *Handlers) CreateCatalog(ctx context.Context, request CreateCatalogRequestObject) (CreateCatalogResponseObject, error) {
	var connID pgtype.UUID
	connID.Bytes = request.ConnectionId
	connID.Valid = true

	var desc *string
	if request.Body.Description != nil {
		desc = request.Body.Description
	}

	catalog, err := h.queries.CreateCatalog(ctx, db.CreateCatalogParams{
		ConnectionID:     connID,
		Name:             request.Body.Name,
		TrinoCatalogName: request.Body.TrinoCatalogName,
		Description:      desc,
	})
	if err != nil {
		return nil, err
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err == nil {
		props := trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, request.Body.Name)
		if props != nil {
			_ = h.trino.CreateCatalog(ctx, trino_client.CatalogParams{
				Name:       request.Body.TrinoCatalogName,
				Connector:  trinoConnector(conn.Type),
				Properties: props,
			})
		}
	}

	id := openapi_types.UUID(catalog.ID.Bytes)
	return CreateCatalog201JSONResponse(Catalog{
		Id:               &id,
		Name:             &catalog.Name,
		TrinoCatalogName: &catalog.TrinoCatalogName,
	}), nil
}

func (h *Handlers) GetCatalog(ctx context.Context, request GetCatalogRequestObject) (GetCatalogResponseObject, error) {
	var catalogID pgtype.UUID
	catalogID.Bytes = request.CatalogId
	catalogID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catalogID)
	if err != nil {
		return GetCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	id := openapi_types.UUID(catalog.ID.Bytes)
	return GetCatalog200JSONResponse(Catalog{
		Id:               &id,
		Name:             &catalog.Name,
		TrinoCatalogName: &catalog.TrinoCatalogName,
	}), nil
}

func (h *Handlers) UpdateCatalog(ctx context.Context, request UpdateCatalogRequestObject) (UpdateCatalogResponseObject, error) {
	var catalogID pgtype.UUID
	catalogID.Bytes = request.CatalogId
	catalogID.Valid = true

	var desc *string
	if request.Body.Description != nil {
		desc = request.Body.Description
	}

	catalog, err := h.queries.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID:               catalogID,
		Name:             request.Body.Name,
		TrinoCatalogName: request.Body.TrinoCatalogName,
		Description:      desc,
	})
	if err != nil {
		return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	id := openapi_types.UUID(catalog.ID.Bytes)
	return UpdateCatalog200JSONResponse(Catalog{
		Id:               &id,
		Name:             &catalog.Name,
		TrinoCatalogName: &catalog.TrinoCatalogName,
	}), nil
}

func (h *Handlers) DeleteCatalog(ctx context.Context, request DeleteCatalogRequestObject) (DeleteCatalogResponseObject, error) {
	var catalogID pgtype.UUID
	catalogID.Bytes = request.CatalogId
	catalogID.Valid = true

	if err := h.queries.DeleteCatalog(ctx, catalogID); err != nil {
		return DeleteCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	return DeleteCatalog204Response{}, nil
}

func (h *Handlers) ListNamespaces(ctx context.Context, request ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	client, catName, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	namespaces, err := client.ListNamespaces(ctx, catName)
	if err != nil {
		return nil, err
	}

	return ListNamespaces200JSONResponse(namespaces), nil
}

func (h *Handlers) ListTables(ctx context.Context, request ListTablesRequestObject) (ListTablesResponseObject, error) {
	client, catName, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	tables, err := client.ListTables(ctx, catName, request.Namespace)
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
	client, catName, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, catName, request.Namespace, request.Table)
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
		typeStr, _ := json.Marshal(f.Type)
		typeStrVal := string(typeStr)
		result = append(result, struct {
			Doc      *string `json:"doc,omitempty"`
			Id       *int    `json:"id,omitempty"`
			Name     *string `json:"name,omitempty"`
			Required *bool   `json:"required,omitempty"`
			Type     *string `json:"type,omitempty"`
		}{Id: &id, Name: &name, Type: &typeStrVal, Required: &required, Doc: &doc})
	}
	return result, nil
}

func (h *Handlers) ListTableSnapshots(ctx context.Context, request ListTableSnapshotsRequestObject) (ListTableSnapshotsResponseObject, error) {
	client, catName, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, catName, request.Namespace, request.Table)
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

func (h *Handlers) resolveCatalogClient(ctx context.Context, catalogID openapi_types.UUID) (catalog_connections.Client, string, error) {
	var catID pgtype.UUID
	catID.Bytes = catalogID
	catID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return nil, "", err
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, catalog.ConnectionID)
	if err != nil {
		return nil, "", err
	}

	client, err := catalog_connections.NewClient(conn.Type, conn.Url, conn.CredentialsEnc)
	if err != nil {
		return nil, "", err
	}

	return client, catalog.Name, nil
}

func trinoConnector(connType string) string {
	switch connType {
	case "postgresql", "postgres":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "polaris", "iceberg", "nessie":
		return "iceberg"
	default:
		return ""
	}
}

func trinoCatalogProperties(connType, url string, credsEnc *string, warehouse string) map[string]string {
	switch connType {
	case "polaris", "iceberg", "nessie":
		if credsEnc == nil {
			return nil
		}
		var creds struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Realm        string `json:"realm"`
		}
		if err := json.Unmarshal([]byte(*credsEnc), &creds); err != nil {
			return nil
		}
		realm := creds.Realm
		if realm == "" {
			realm = "POLARIS"
		}
		return map[string]string{
			"iceberg.catalog.type":                     "rest",
			"iceberg.rest-catalog.uri":                  url + "/api/catalog",
			"iceberg.rest-catalog.warehouse":            warehouse,
			"iceberg.rest-catalog.security":             "OAUTH2",
			"iceberg.rest-catalog.oauth2.credential":    creds.ClientID + ":" + creds.ClientSecret,
			"iceberg.rest-catalog.oauth2.scope":         "PRINCIPAL_ROLE:ALL",
			"iceberg.rest-catalog.vended-credentials-enabled": "true",
			"fs.native-s3.enabled":                      "true",
		}
	case "postgresql", "mysql":
		props := map[string]string{
			"connection-url": url,
		}
		if credsEnc != nil {
			var creds struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal([]byte(*credsEnc), &creds); err == nil {
				props["connection-user"] = creds.Username
				props["connection-password"] = creds.Password
			}
		}
		return props
	default:
		return nil
	}
}
