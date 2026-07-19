package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
	"log/slog"

	catalog_connections "github.com/galileostudio/baleia/internal/catalog_connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/google/uuid"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	trino_client "github.com/galileostudio/baleia/internal/trino"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		bodyJSON, _ := json.Marshal(request.Body)
		slog.Info("polaris case hit", "creds_nil", request.Body.PolarisCredentials == nil, "creds_generic", request.Body.Credentials != nil, "body", string(bodyJSON))
		credsJSON := marshalCreds(request.Body.PolarisCredentials, request.Body.Credentials)
		if credsJSON != nil {
			enc, err := crypto.Encrypt(credsJSON, h.cfg.JWTSecret)
			if err != nil {
				slog.Error("polaris encrypt failed", "err", err)
			} else {
				credsEnc = &enc
			}
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return catalogConnectionConflictJSONResponse{Message: "a connection with this name already exists"}, nil
		}
		return nil, err
	}

	h.catalogCache.Clear()

	return CreateCatalogConnection201JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func marshalCreds(typed interface{}, generic *map[string]interface{}) []byte {
	if typed != nil {
		b, err := json.Marshal(typed)
		if err == nil && string(b) != "null" {
			slog.Info("creds: using typed credentials")
			return b
		}
		slog.Warn("creds: typed marshal failed or nil, falling through", "err", err, "json", string(b))
	}
	if generic != nil && len(*generic) > 0 {
		b, err := json.Marshal(generic)
		if err == nil {
			slog.Info("creds: using generic credentials", "fields", len(*generic))
			return b
		}
		slog.Warn("creds: generic marshal failed, falling through", "err", err)
	}
	if cid := os.Getenv("POLARIS_CLIENT_ID"); cid != "" {
		if cs := os.Getenv("POLARIS_CLIENT_SECRET"); cs != "" {
			m := map[string]string{"client_id": cid, "client_secret": cs}
			b, _ := json.Marshal(m)
			slog.Info("creds: using env var fallback")
			return b
		}
	}
	slog.Warn("creds: no credentials found from any source")
	return nil
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

	h.catalogCache.Clear()

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

    client, err := h.connectionClient(conn)
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: 0, Error: &errMsg}, nil
    }

    testErr := client.Ping(ctx)
    latencyMs := int(time.Since(start).Milliseconds())
    if testErr != nil {
        errMsg := testErr.Error()
        return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: latencyMs, Error: &errMsg}, nil
    }

    v := conn.Type
    return TestCatalogConnection200JSONResponse{Success: true, LatencyMs: latencyMs, Version: &v}, nil
}

func (h *Handlers) testSQLViaTrino(ctx context.Context, conn db.CatalogConnection, start time.Time) (TestCatalogConnectionResponseObject, error) {
    testName := "baleia_test_" + uuid.NewString()[:8]
    props := h.trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, testName)
    if props == nil {
        errMsg := "failed to build catalog properties (check credentials)"
        return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: 0, Error: &errMsg}, nil
    }
    trinoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
    defer cancel()
    err := h.trino.CreateCatalog(trinoCtx, trino_client.CatalogParams{
        Name:       testName,
        Connector:  trinoConnector(conn.Type),
        Properties: props,
    })
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg}, nil
    }
    defer h.trino.Execute(ctx, "DROP CATALOG IF EXISTS "+testName, "", "")
    _, err = h.trino.Execute(trinoCtx, "SHOW SCHEMAS FROM "+testName, testName, "")
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg}, nil
    }
    latencyMs := int(time.Since(start).Milliseconds())
    v := conn.Type
    return TestCatalogConnection200JSONResponse{Success: true, LatencyMs: latencyMs, Version: &v}, nil
}

func (h *Handlers) connectionClient(conn db.CatalogConnection) (catalog_connections.Client, error) {
	var rawCreds *string
	if conn.CredentialsEnc != nil {
		b := crypto.TryDecrypt(*conn.CredentialsEnc, h.cfg.JWTSecret)
		s := string(b); rawCreds = &s
	}
	return catalog_connections.NewClient(conn.Type, conn.Url, rawCreds)
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

	conn, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		return ListCatalogs200JSONResponse([]Catalog{}), nil
	}

	names, err := h.ensureTrinoCatalogs(ctx, conn)
	if err != nil {
		slog.Warn("register catalog in trino", "err", err, "conn", conn.ID.String())
		return ListCatalogs200JSONResponse([]Catalog{}), nil
	}

	result := make([]Catalog, 0, len(names))
	for _, n := range names {
		name := n
		result = append(result, Catalog{Name: &name})
	}
	return ListCatalogs200JSONResponse(result), nil
}

func (h *Handlers) CreateCatalog(ctx context.Context, request CreateCatalogRequestObject) (CreateCatalogResponseObject, error) {
	return nil, fmt.Errorf("catalog creation is no longer supported; catalogs are loaded dynamically")
}

func (h *Handlers) GetCatalog(ctx context.Context, request GetCatalogRequestObject) (GetCatalogResponseObject, error) {
	return GetCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found (dynamic)"}}, nil
}

func (h *Handlers) UpdateCatalog(ctx context.Context, request UpdateCatalogRequestObject) (UpdateCatalogResponseObject, error) {
	return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found (dynamic)"}}, nil
}

func (h *Handlers) DeleteCatalog(ctx context.Context, request DeleteCatalogRequestObject) (DeleteCatalogResponseObject, error) {
	return DeleteCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found (dynamic)"}}, nil
}

func (h *Handlers) ListNamespaces(ctx context.Context, request ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	return ListNamespaces404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "use /connections/{id}/catalogs/{name}/namespaces"}}, nil
}

func (h *Handlers) ListTables(ctx context.Context, request ListTablesRequestObject) (ListTablesResponseObject, error) {
	return ListTables404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "use /connections/{id}/catalogs/{name}/namespaces/{ns}/tables"}}, nil
}

func (h *Handlers) ListColumns(ctx context.Context, request ListColumnsRequestObject) (ListColumnsResponseObject, error) {
	return ListColumns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "use /connections/{id}/catalogs/{name}/namespaces/{ns}/tables/{table}/columns"}}, nil
}

func (h *Handlers) ListTableSnapshots(ctx context.Context, request ListTableSnapshotsRequestObject) (ListTableSnapshotsResponseObject, error) {
	return ListTableSnapshots404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalogs are loaded dynamically"}}, nil
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

// ── Dynamic catalog browsing (chi handlers) ──────────────────────────

type workspaceCatalogEntry struct {
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	ConnectionType string `json:"connection_type"`
	CatalogName    string `json:"catalog_name"`
}

func (h *Handlers) discoverCatalogs(ctx context.Context, conn db.CatalogConnection) ([]string, error) {
	client, err := h.connectionClient(conn)
	if err != nil {
		return nil, err
	}
	remoteCatalogs, err := client.ListCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(remoteCatalogs))
	for _, c := range remoteCatalogs {
		names = append(names, c.Name)
	}
	return names, nil
}

func (h *Handlers) ensureTrinoCatalogs(ctx context.Context, conn db.CatalogConnection) ([]string, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connector := trinoConnector(conn.Type)
	if connector == "" {
		return nil, fmt.Errorf("no trino connector for type: %s", conn.Type)
	}

	var catalogNames []string
	if conn.Type == "polaris" || conn.Type == "nessie" || conn.Type == "iceberg" {
		dcCtx, dcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcCancel()
		names, err := h.discoverCatalogs(dcCtx, conn)
		if err != nil {
			slog.Warn("catalog discovery failed, using connection name", "conn", conn.Name, "err", err)
			catalogNames = []string{conn.Name}
		} else if len(names) == 0 {
			slog.Warn("no catalogs discovered, using connection name", "conn", conn.Name)
			catalogNames = []string{conn.Name}
		} else {
			catalogNames = names
		}
	} else {
		catalogNames = []string{conn.Name}
	}

	for _, cn := range catalogNames {
		props := h.trinoCatalogProperties(conn.Type, conn.Url, conn.CredentialsEnc, cn)
		if props == nil {
			slog.Warn("skip catalog: failed to build props", "conn", conn.Name, "catalog", cn)
			continue
		}
		if err := h.trino.CreateCatalog(bgCtx, trino_client.CatalogParams{
			Name:       cn,
			Connector:  connector,
			Properties: props,
		}); err != nil {
			slog.Warn("skip catalog: trino registration failed", "conn", conn.Name, "catalog", cn, "err", err)
			continue
		}
	}

	if len(catalogNames) == 0 {
		return nil, fmt.Errorf("no catalogs could be registered for %s", conn.Name)
	}
	return catalogNames, nil
}

func (h *Handlers) ListWorkspaceCatalogs(w http.ResponseWriter, r *http.Request) {
	wsID := chiURLParam(r, "id")
	userID := auth.UserIDFromContext(r.Context())

	cacheKey := wsID + ":" + userID
	if cached, ok := h.catalogCache.Get(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}

	var wsUUID pgtype.UUID
	if err := wsUUID.Scan(wsID); err != nil {
		http.Error(w, `{"code":"bad_request","message":"invalid workspace id"}`, http.StatusBadRequest)
		return
	}

	ws, err := h.queries.GetWorkspaceByID(r.Context(), wsUUID)
	if err != nil {
		http.Error(w, `{"code":"not_found","message":"workspace not found"}`, http.StatusNotFound)
		return
	}

	if ws.OwnerUserID.Valid {
		uid, _ := uuidFromString(userID)
		if ws.OwnerUserID.Bytes != uid.Bytes {
			http.Error(w, `{"code":"forbidden","message":"not your workspace"}`, http.StatusForbidden)
			return
		}
	}

	conns, err := h.queries.ListCatalogConnections(r.Context(), ws.OwnerUserID)
	if err != nil {
		slog.Error("list connections for workspace", "err", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]workspaceCatalogEntry{})
		return
	}

	var entries []workspaceCatalogEntry
	for _, conn := range conns {
		catalogNames, err := h.ensureTrinoCatalogs(r.Context(), conn)
		if err != nil {
			slog.Warn("skip connection in workspace catalogs", "conn", conn.Name, "err", err)
			continue
		}
		for _, cn := range catalogNames {
			entries = append(entries, workspaceCatalogEntry{
				ConnectionID:   uuidStr(conn.ID),
				ConnectionName: conn.Name,
				ConnectionType: conn.Type,
				CatalogName:    cn,
			})
		}
	}

	if entries == nil {
		entries = []workspaceCatalogEntry{}
	}
	h.catalogCache.Set(cacheKey, entries)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (h *Handlers) HandleListNamespaces(w http.ResponseWriter, r *http.Request) {
	connID := chiURLParam(r, "connection_id")
	catalogName := chiURLParam(r, "catalog_name")

	if _, err := h.ensureConnectionAndCatalog(r.Context(), connID, catalogName); err != nil {
		slog.Warn("list namespaces: ensure failed", "conn", connID, "catalog", catalogName, "err", err)
		http.Error(w, `{"code":"connection_error","message":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	qCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := h.trino.Execute(qCtx, "SHOW SCHEMAS FROM "+catalogName, catalogName, "")
	if err != nil {
		slog.Error("list schemas via trino", "err", err, "catalog", catalogName)
		http.Error(w, `{"code":"error","message":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	namespaces := make([][]string, 0, len(result.Data))
	for _, row := range result.Data {
		if len(row) > 0 {
			namespaces = append(namespaces, []string{fmt.Sprint(row[0])})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(namespaces)
}

func (h *Handlers) HandleListTables(w http.ResponseWriter, r *http.Request) {
	connID := chiURLParam(r, "connection_id")
	catalogName := chiURLParam(r, "catalog_name")
	namespace := chiURLParam(r, "namespace")

	if _, err := h.ensureConnectionAndCatalog(r.Context(), connID, catalogName); err != nil {
		slog.Warn("list tables: ensure failed", "conn", connID, "catalog", catalogName, "err", err)
		http.Error(w, `{"code":"connection_error","message":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	qCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := h.trino.Execute(qCtx, "SHOW TABLES FROM "+catalogName+"."+namespace, catalogName, namespace)
	if err != nil {
		slog.Error("list tables via trino", "err", err)
		http.Error(w, `{"code":"error","message":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	type tableEntry struct {
		Name      *string   `json:"name,omitempty"`
		Namespace *[]string `json:"namespace,omitempty"`
	}
	tables := make([]tableEntry, 0, len(result.Data))
	for _, row := range result.Data {
		if len(row) > 0 {
			name := fmt.Sprint(row[0])
			ns := []string{namespace}
			tables = append(tables, tableEntry{Name: &name, Namespace: &ns})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (h *Handlers) HandleListColumns(w http.ResponseWriter, r *http.Request) {
	connID := chiURLParam(r, "connection_id")
	catalogName := chiURLParam(r, "catalog_name")
	namespace := chiURLParam(r, "namespace")
	table := chiURLParam(r, "table")

	if _, err := h.ensureConnectionAndCatalog(r.Context(), connID, catalogName); err != nil {
		slog.Warn("list columns: ensure failed", "conn", connID, "catalog", catalogName, "err", err)
		http.Error(w, `{"code":"connection_error","message":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	qCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	meta, err := h.trino.Execute(qCtx, "DESCRIBE "+catalogName+"."+namespace+"."+table, catalogName, namespace)
	if err != nil {
		http.Error(w, `{"code":"error","message":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	type colEntry struct {
		Name *string `json:"name,omitempty"`
		Type *string `json:"type,omitempty"`
	}
	cols := make([]colEntry, 0, len(meta.Data))
	for _, row := range meta.Data {
		if len(row) >= 2 {
			name := fmt.Sprint(row[0])
			typ := fmt.Sprint(row[1])
			cols = append(cols, colEntry{Name: &name, Type: &typ})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cols)
}

func (h *Handlers) ensureConnectionAndCatalog(ctx context.Context, connID, catalogName string) (*db.CatalogConnection, error) {
	var id pgtype.UUID
	if err := id.Scan(connID); err != nil {
		return nil, err
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := h.ensureTrinoCatalogs(ctx, conn); err != nil {
		return nil, err
	}
	return &conn, nil
}

func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

func uuidStr(u pgtype.UUID) string {
	return openapi_types.UUID(u.Bytes).String()
}

type catalogConnectionConflictJSONResponse struct {
	Message string `json:"message"`
}

func (r catalogConnectionConflictJSONResponse) VisitCreateCatalogConnectionResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	return json.NewEncoder(w).Encode(r)
}
