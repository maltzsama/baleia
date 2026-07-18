package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"log/slog"
	
	catalog_connections "github.com/galileostudio/baleia/internal/catalog_connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/google/uuid"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	trino_client "github.com/galileostudio/baleia/internal/trino"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return ListCatalogConnections401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	conns, err := h.queries.ListCatalogConnections(ctx, uid)
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
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	var credsEnc *string
	switch string(request.Body.Type) {
	case "polaris":
		if request.Body.PolarisCredentials != nil {
			b, err := json.Marshal(request.Body.PolarisCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "nessie":
		if request.Body.NessieCredentials != nil {
			b, err := json.Marshal(request.Body.NessieCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "glue":
		if request.Body.GlueCredentials != nil {
			b, err := json.Marshal(request.Body.GlueCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "hive":
		if request.Body.HiveCredentials != nil {
			b, err := json.Marshal(request.Body.HiveCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "iceberg":
		if request.Body.IcebergCredentials != nil {
			b, err := json.Marshal(request.Body.IcebergCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "postgresql":
		if request.Body.PostgresqlCredentials != nil {
			b, err := json.Marshal(request.Body.PostgresqlCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
		}
	case "mysql":
		if request.Body.MysqlCredentials != nil {
			b, err := json.Marshal(request.Body.MysqlCredentials)
			if err != nil { return nil, err }
			enc, _ := crypto.Encrypt(b, h.cfg.JWTSecret); credsEnc = &enc
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
		CreatedBy:       uid,
	})
	if err != nil {
		slog.Error("CreateCatalogConnection failed", "err", err)
		return nil, err
	}

	if conn.Type == "postgresql" || conn.Type == "mysql" {
		cat := h.createSQLCatalog(ctx, conn)
		if cat != nil {
			ws := h.ensurePersonalWorkspace(ctx, uid)
			if ws != nil {
				_, err := h.queries.GrantCatalogAccess(ctx, db.GrantCatalogAccessParams{
					WorkspaceID: ws.ID,
					CatalogID:   cat.ID,
					Permission:  "read",
					GrantedBy:   uid,
				})
				if err != nil {
					slog.Warn("auto-grant catalog failed", "err", err, "catalog", cat.ID.String(), "workspace", ws.ID.String())
				}
			}
		}
	}

	return CreateCatalogConnection201JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) createSQLCatalog(ctx context.Context, conn db.CatalogConnection) *db.Catalog {
	catalogName := extractDBName(conn.Url)
	if catalogName == "" {
		catalogName = conn.Name
	}

	cat, err := h.queries.CreateCatalog(ctx, db.CreateCatalogParams{
		ConnectionID: conn.ID,
		Name:         catalogName,
	})
	if err != nil {
		slog.Warn("auto-create catalog failed (DB)", "err", err, "conn", conn.ID.String())
		return nil
	}

	props := h.trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, catalogName)
	if props != nil {
		if err := h.trino.CreateCatalog(ctx, trino_client.CatalogParams{
			Name:       catalogName,
			Connector:  trinoConnector(conn.Type),
			Properties: props,
		}); err != nil {
			slog.Warn("auto-create catalog failed (Trino)", "err", err, "conn", conn.ID.String(), "catalog", catalogName)
		}
	}

	return &cat
}

func extractDBName(rawURL string) string {
	s := strings.TrimPrefix(rawURL, "jdbc:")
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	name := strings.TrimPrefix(u.Path, "/")
	if idx := strings.IndexByte(name, '?'); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return GetCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	resp := dbCatalogConnectionToPublic(conn)
	if conn.CredentialsEnc != nil {
		b := crypto.TryDecrypt(*conn.CredentialsEnc, h.cfg.JWTSecret)
		var creds map[string]interface{}
		if json.Unmarshal(b, &creds) == nil {
			resp.Credentials = &creds
		}
	}
	return GetCatalogConnection200JSONResponse(resp), nil
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	var connID pgtype.UUID
	connID.Bytes = request.Id
	connID.Valid = true

	current, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		return UpdateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	if current.CreatedBy.Valid && current.CreatedBy.Bytes != uid.Bytes {
		return UpdateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not your connection"}}, nil
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
		ID:              connID,
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
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return DeleteCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	var connID pgtype.UUID
	connID.Bytes = request.Id
	connID.Valid = true

	current, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		return DeleteCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	if current.CreatedBy.Valid && current.CreatedBy.Bytes != uid.Bytes {
		return DeleteCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not your connection"}}, nil
	}

	if err := h.queries.DeleteCatalogConnection(ctx, connID); err != nil {
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

    if conn.Type == "postgresql" || conn.Type == "mysql" {
        return h.testSQLViaTrino(ctx, conn, start)
    }

    var rawCreds *string
    if conn.CredentialsEnc != nil {
        b := crypto.TryDecrypt(*conn.CredentialsEnc, h.cfg.JWTSecret)
        s := string(b); rawCreds = &s
    }
    client, err := catalog_connections.NewClient(conn.Type, conn.Url, rawCreds)
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

func (h *Handlers) testSQLViaTrino(ctx context.Context, conn db.CatalogConnection, start time.Time) (TestCatalogConnectionResponseObject, error) {
    testName := "baleia_test_" + uuid.NewString()[:8]

    props := h.trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, testName)
    if props == nil {
        errMsg := "failed to build catalog properties"
        return TestCatalogConnection200JSONResponse{
            Success: false, LatencyMs: 0, Error: &errMsg,
        }, nil
    }

    err := h.trino.CreateCatalog(ctx, trino_client.CatalogParams{
        Name:       testName,
        Connector:  trinoConnector(conn.Type),
        Properties: props,
    })
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{
            Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg,
        }, nil
    }

    defer h.trino.Execute(ctx, "DROP CATALOG IF EXISTS "+testName, "", "")

    _, err = h.trino.Execute(ctx, "SHOW SCHEMAS FROM "+testName, testName, "")
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{
            Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg,
        }, nil
    }

    latencyMs := int(time.Since(start).Milliseconds())
    v := conn.Type
    return TestCatalogConnection200JSONResponse{
        Success: true, LatencyMs: latencyMs, Version: &v,
    }, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *Handlers) ensurePersonalWorkspace(ctx context.Context, userID pgtype.UUID) *db.Workspace {
	workspaces, err := h.queries.ListWorkspacesByUser(ctx, userID)
	if err == nil {
		for _, w := range workspaces {
			if w.Type == "personal" && w.OwnerUserID.Valid && w.OwnerUserID.Bytes == userID.Bytes {
				return &w
			}
		}
	}

	polarisName := fmt.Sprintf("personal_%x_%d", userID.Bytes[:8], time.Now().UnixMilli()%10000)

	wsName := "Personal"
	if user, err := h.queries.GetUserByID(ctx, userID); err == nil && user.Name != "" {
		wsName = user.Name + " - workspace"
	}

	ws, err := h.queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               wsName,
		Type:               "personal",
		PolarisCatalogName: polarisName,
		OwnerUserID:        userID,
	})
	if err != nil {
		slog.Warn("auto-create personal workspace failed", "err", err)
		return nil
	}
	return &ws
}

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
		
		Description:      desc,
	})
	if err != nil {
		return nil, err
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err == nil {
		props := h.trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, request.Body.Name)
		if props != nil {
			_ = h.trino.CreateCatalog(ctx, trino_client.CatalogParams{
				Name:       request.Body.Name,
				Connector:  trinoConnector(conn.Type),
				Properties: props,
			})
		}
	}

	id := openapi_types.UUID(catalog.ID.Bytes)
	return CreateCatalog201JSONResponse(Catalog{
		Id:               &id,
		Name:             &catalog.Name,
		
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
		
		Description:      desc,
	})
	if err != nil {
		return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	id := openapi_types.UUID(catalog.ID.Bytes)
	return UpdateCatalog200JSONResponse(Catalog{
		Id:               &id,
		Name:             &catalog.Name,
		
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
	var catID pgtype.UUID
	catID.Bytes = request.CatalogId
	catID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, catalog.ConnectionID)
	if err != nil {
		return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "connection not found"}}, nil
	}

	if conn.Type == "postgresql" || conn.Type == "mysql" {
		result, err := h.trino.Execute(ctx, "SHOW SCHEMAS FROM "+catalog.Name, catalog.Name, "")
		if err != nil {
			return nil, err
		}
		namespaces := make([][]string, 0, len(result.Data))
		for _, row := range result.Data {
			if len(row) > 0 {
				namespaces = append(namespaces, []string{fmt.Sprint(row[0])})
			}
		}
		return ListNamespaces200JSONResponse(namespaces), nil
	}

	client, _, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	namespaces, err := client.ListNamespaces(ctx, catalog.Name)
	if err != nil {
		return nil, err
	}

	return ListNamespaces200JSONResponse(namespaces), nil
}

func (h *Handlers) ListTables(ctx context.Context, request ListTablesRequestObject) (ListTablesResponseObject, error) {
	var catID pgtype.UUID
	catID.Bytes = request.CatalogId
	catID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, catalog.ConnectionID)
	if err != nil {
		return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "connection not found"}}, nil
	}

	if conn.Type == "postgresql" || conn.Type == "mysql" {
		result, err := h.trino.Execute(ctx, "SHOW TABLES FROM "+catalog.Name+"."+request.Namespace, catalog.Name, request.Namespace)
		if err != nil {
			return nil, err
		}
		list := make(ListTables200JSONResponse, 0, len(result.Data))
		for _, row := range result.Data {
			if len(row) > 0 {
				name := fmt.Sprint(row[0])
				ns := []string{request.Namespace}
				list = append(list, struct {
					Name      *string   `json:"name,omitempty"`
					Namespace *[]string `json:"namespace,omitempty"`
				}{Name: &name, Namespace: &ns})
			}
		}
		return list, nil
	}

	client, _, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	tables, err := client.ListTables(ctx, catalog.Name, request.Namespace)
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
	var catID pgtype.UUID
	catID.Bytes = request.CatalogId
	catID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, catalog.ConnectionID)
	if err != nil {
		return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "connection not found"}}, nil
	}

	if conn.Type == "postgresql" || conn.Type == "mysql" {
		return listColumnsViaTrino(ctx, h, catalog.Name, request.Namespace, request.Table)
	}

	client, _, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, catalog.Name, request.Namespace, request.Table)
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

func listColumnsViaTrino(ctx context.Context, h *Handlers, catalog, schema, table string) (ListColumns200JSONResponse, error) {
	result, err := h.trino.Execute(ctx, "DESCRIBE "+catalog+"."+schema+"."+table, catalog, schema)
	if err != nil {
		return nil, err
	}
	cols := make(ListColumns200JSONResponse, 0, len(result.Data))
	for _, row := range result.Data {
		if len(row) >= 2 {
			name := fmt.Sprint(row[0])
			typ := fmt.Sprint(row[1])
			cols = append(cols, struct {
				Doc      *string `json:"doc,omitempty"`
				Id       *int    `json:"id,omitempty"`
				Name     *string `json:"name,omitempty"`
				Required *bool   `json:"required,omitempty"`
				Type     *string `json:"type,omitempty"`
			}{Name: &name, Type: &typ})
		}
	}
	return cols, nil
}

func (h *Handlers) ListTableSnapshots(ctx context.Context, request ListTableSnapshotsRequestObject) (ListTableSnapshotsResponseObject, error) {
	var catID pgtype.UUID
	catID.Bytes = request.CatalogId
	catID.Valid = true

	catalog, err := h.queries.GetCatalogByID(ctx, catID)
	if err != nil {
		return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	conn, err := h.queries.GetCatalogConnectionByID(ctx, catalog.ConnectionID)
	if err != nil {
		return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "connection not found"}}, nil
	}

	if conn.Type == "postgresql" || conn.Type == "mysql" {
		return ListTableSnapshots200JSONResponse{}, nil
	}

	client, _, err := h.resolveCatalogClient(ctx, request.CatalogId)
	if err != nil {
		return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: err.Error()}}, nil
	}

	meta, err := client.LoadTable(ctx, catalog.Name, request.Namespace, request.Table)
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

	var rawCreds *string
	if conn.CredentialsEnc != nil {
		b := crypto.TryDecrypt(*conn.CredentialsEnc, h.cfg.JWTSecret)
		s := string(b); rawCreds = &s
	}

	client, err := catalog_connections.NewClient(conn.Type, conn.Url, rawCreds)
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

func (h *Handlers) trinoCatalogProperties(connType, url string, credsEnc *string, warehouse string) map[string]string {
	var credsBytes []byte
	if credsEnc != nil {
		credsBytes = crypto.TryDecrypt(*credsEnc, h.cfg.JWTSecret)
	}
	switch connType {
	case "polaris", "iceberg", "nessie":
		if credsBytes == nil {
			return nil
		}
		var creds struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Realm        string `json:"realm"`
		}
		if err := json.Unmarshal(credsBytes, &creds); err != nil {
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
		if credsBytes != nil {
			var creds struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal(credsBytes, &creds); err == nil {
				props["connection-user"] = creds.Username
				props["connection-password"] = creds.Password
			}
		}
		return props
	default:
		return nil
	}
}
