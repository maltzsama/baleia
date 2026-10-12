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
	catalogs "github.com/galileostudio/baleia/internal/catalogs"
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

	params, err := buildConnectionUpdateParams(connID, *request.Body, h.cfg.JWTSecret)
	if err != nil {
		return nil, err
	}

	conn, err := h.queries.UpdateCatalogConnection(ctx, params)
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

// testSQLViaTrino registers a throwaway catalog in Trino using catalogs.Project
// (the canonical property builder) + @baleia-secret[...] placeholder, runs a
// SHOW SCHEMAS as a smoke test, then DROPs the catalog and the throwaway
// registry row. L2 (CR-017): Polaris connections are tested via REST Ping at
// the TestCatalogConnection level, so this path is only used for postgresql/
// mysql connections today.
//
// Note: the placeholder the DDL carries resolves against the catalog name we
// just registered in trino_catalog_registry. So we must write a temporary
// registry row BEFORE emitting the DDL — the plugin reads it at boot-time
// AND at createCatalogProperties-time. For the test, we upsert a row with
// sync_status='pending', issue the CREATE CATALOG, then DROP CATALOG and
// soft-delete the registry row.
func (h *Handlers) testSQLViaTrino(ctx context.Context, conn db.CatalogConnection, start time.Time) (TestCatalogConnectionResponseObject, error) {
	testName := "baleia_test_" + uuid.NewString()[:8]

	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("get default cluster: %w", err)
	}
	creds, err := catalogs.DecodeCredentials(conn, h.cfg.JWTSecret)
	if err != nil {
		errMsg := err.Error()
		return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: 0, Error: &errMsg}, nil
	}

	// Build a throwaway catalog row for Project to consume. Use a bogus
	// connection id pointing back at conn so Project works.
	throw := db.Catalog{
		ConnectionID:      conn.ID,
		TrinoCatalogName: testName,
		ClusterID:         cluster.ID,
		Warehouse:         testName,
		Connector:         trinoConnector(conn.Type),
	}
	proj, err := catalogs.Project(conn, throw, creds)
	if err != nil {
		errMsg := err.Error()
		return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: 0, Error: &errMsg}, nil
	}
	propsJSON, _ := json.Marshal(proj.Row.Properties)
	_ = h.queries.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID:      pgtype.UUID{Bytes: proj.Row.ClusterID, Valid: true},
		CatalogName:    proj.Row.CatalogName,
		ConnectorName:  proj.Row.ConnectorName,
		Column4:        propsJSON,
	})
	defer func() {
		_ = h.queries.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{
			ClusterID:   cluster.ID,
			CatalogName: testName,
		})
		_ = h.trino.DropCatalog(ctx, testName)
	}()

	trinoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := h.trino.CreateCatalog(trinoCtx, trino_client.CatalogSpec{
		Name:       proj.Row.CatalogName,
		Connector:  proj.Row.ConnectorName,
		Properties: proj.Row.Properties,
		SecretKeys: proj.SecretKeys,
	}); err != nil {
		errMsg := err.Error()
		return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg}, nil
	}

	if _, err := h.trino.Execute(trinoCtx, "SHOW SCHEMAS FROM "+testName, testName, ""); err != nil {
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

// Catalog handlers (ListCatalogs, CreateCatalog, GetCatalog, UpdateCatalog,
// DeleteCatalog, SyncCatalog, ReconcileTrino) and the write-through flow
// (Project + UpsertRegistry + DDL) live in handlers_catalogs_write.go.

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

// trinoCatalogProperties was the old inline builder for connector properties
// from the pre-plugin era. It has been replaced by internal/catalogs.Project,
// which lives alongside the write-through handlers in handlers_catalogs_write.go.
// (CR-017 §L1: the dynamic catalog-discovery path via Polaris REST has been
// removed — catalogs are now discovered via the registry table that the
// plugin loads at Trino boot.)

// workspaceCatalogEntry describes one catalog-row in the per-workspace
// listing exposed to the UI. Fields mirror what the frontend DataSourceTree
// expects.
type workspaceCatalogEntry struct {
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	ConnectionType string `json:"connection_type"`
	CatalogName    string `json:"catalog_name"`
}

// ListWorkspaceCatalogs lists catalogs visible to a workspace. Source of
// truth is the catalogs table in PostgreSQL; the plugin loads them into
// Trino at boot. No more dynamic discovery via Polaris REST, no more
// ensureTrinoCatalogs — that was the path L1 of CR-017 removed.
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
		// The catalogs table is the source of truth — no Polaris REST call.
		rows, err := h.queries.ListCatalogsByConnection(r.Context(), conn.ID)
		if err != nil {
			slog.Warn("list catalogs for connection", "conn", conn.Name, "err", err)
			continue
		}
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			entries = append(entries, workspaceCatalogEntry{
				ConnectionID:   uuidStr(conn.ID),
				ConnectionName: conn.Name,
				ConnectionType: conn.Type,
				CatalogName:    row.TrinoCatalogName,
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

// ensureConnectionAndCatalog fetches the catalog connection row and verifies
// that there is an enabled catalogs row matching the requested trino_catalog_name.
// The plugin already loaded the catalog into Trino at boot (or it will after
// the next Trino restart) — no CREATE CATALOG is issued here.
func (h *Handlers) ensureConnectionAndCatalog(ctx context.Context, connID, catalogName string) (*db.CatalogConnection, error) {
	var id pgtype.UUID
	if err := id.Scan(connID); err != nil {
		return nil, err
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := h.queries.GetCatalogByTrinoName(ctx, db.GetCatalogByTrinoNameParams{
		ClusterID:        cluster.ID,
		TrinoCatalogName: catalogName,
	}); err != nil {
		return nil, fmt.Errorf("catalog %q not registered under connection %s", catalogName, conn.Name)
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
