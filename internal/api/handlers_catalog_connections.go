package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"log/slog"

	catalog_connections "github.com/galileostudio/baleia/internal/catalog_connections"
	catalogs "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/google/uuid"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	trino_client "github.com/galileostudio/baleia/internal/trino"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
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

	// The connection id is generated here, not by the database default: the
	// credentials are sealed with it as additional authenticated data.
	connID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	credsEnc, err := encodeCredentials(h.keyring, createCredentials(request.Body), connID)
	if err != nil {
		slog.Error("encrypt connection credentials", "err", err)
		return catalogConnectionEncryptionFailedJSONResponse{Code: "encryption_failed", Message: "could not encrypt the connection credentials"}, nil
	}

	defaultReadonly := true
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}

	conn, err := h.queries.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID:              connID,
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

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	// Owner or global admin only; anyone else gets 404 so existence is not
	// revealed.
	conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return GetCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	// Never return the stored secret. Callers see only has_credentials.
	return GetCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	connID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.authz().Connection(ctx, p, connID, authz.ActWrite); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return UpdateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	params, err := buildConnectionUpdateParams(connID, *request.Body, h.keyring)
	if err != nil {
		slog.Error("encrypt connection credentials", "err", err)
		return catalogConnectionEncryptionFailedJSONResponse{Code: "encryption_failed", Message: "could not encrypt the connection credentials"}, nil
	}

	conn, err := h.queries.UpdateCatalogConnection(ctx, params)
	if err != nil {
		return nil, err
	}

	h.catalogCache.Clear()

	return UpdateCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	connID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.authz().Connection(ctx, p, connID, authz.ActAdmin); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return DeleteCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	if err := h.queries.DeleteCatalogConnection(ctx, connID); err != nil {
		return nil, err
	}

	h.catalogCache.Clear()

	return DeleteCatalogConnection204Response{}, nil
}

func (h *Handlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return TestCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	// Only the owner (or a global admin) may trigger a probe with the stored
	// credentials; checked before any outbound call.
	conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActWrite)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return TestCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		return nil, err
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
	creds, err := catalogs.DecodeCredentials(conn, h.keyring)
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

	testTarget, err := trino_client.QualifiedName(testName)
	if err != nil {
		return nil, err
	}
	if _, err := h.trino.Execute(trinoCtx, "SHOW SCHEMAS FROM "+testTarget, testName, ""); err != nil {
		errMsg := err.Error()
		return TestCatalogConnection200JSONResponse{Success: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: &errMsg}, nil
	}

	latencyMs := int(time.Since(start).Milliseconds())
	v := conn.Type
	return TestCatalogConnection200JSONResponse{Success: true, LatencyMs: latencyMs, Version: &v}, nil
}

func (h *Handlers) connectionClient(conn db.CatalogConnection) (catalog_connections.Client, error) {
	plain, err := catalogs.OpenCredentials(conn, h.keyring)
	if err != nil {
		return nil, err
	}
	var rawCreds *string
	if plain != nil {
		s := string(plain)
		rawCreds = &s
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
		Url:             redactURLSecrets(c.Url),
		HasCredentials:  c.CredentialsEnc != nil && *c.CredentialsEnc != "",
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

// writeAPIError answers with a JSON error built by json.Marshal. The cause is
// logged, never returned: Trino and database error text can carry identifiers,
// paths and credentials.
func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string, cause error) {
	rid := middleware.GetReqID(r.Context())
	if cause != nil {
		slog.Error("explorer request failed", "code", code, "request_id", rid, "err", cause)
	}
	writeJSON(w, status, map[string]string{"code": code, "message": message, "request_id": rid})
}

// explorerTarget authorizes an explorer request and returns the catalog it
// addresses. The catalog must belong to the connection in the path, and the
// caller must be allowed to read it; anything else is 404 (no existence oracle).
// It writes the error response itself and returns ok=false.
func (h *Handlers) explorerTarget(w http.ResponseWriter, r *http.Request) (db.Catalog, bool) {
	ctx := r.Context()
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "invalid user", nil)
		return db.Catalog{}, false
	}
	var connID pgtype.UUID
	if err := connID.Scan(chiURLParam(r, "connection_id")); err != nil || !connID.Valid {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "catalog not found", nil)
		return db.Catalog{}, false
	}
	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "internal error", err)
		return db.Catalog{}, false
	}
	found, err := h.queries.GetCatalogByTrinoName(ctx, db.GetCatalogByTrinoNameParams{
		ClusterID:        cluster.ID,
		TrinoCatalogName: chiURLParam(r, "catalog_name"),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "catalog not found", nil)
		} else {
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "internal error", err)
		}
		return db.Catalog{}, false
	}
	cat, _, err := h.authz().CatalogOfConnection(ctx, p, connID, found.ID, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "catalog not found", nil)
		} else {
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "internal error", err)
		}
		return db.Catalog{}, false
	}
	return cat, true
}

// exploreQuery runs one metadata statement under the request context with a
// 30 s deadline, so a client disconnect cancels the Trino call.
func (h *Handlers) exploreQuery(r *http.Request, sql string) (*trino_client.QueryResult, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	return h.trino.Execute(ctx, sql, "", "")
}

func (h *Handlers) HandleListNamespaces(w http.ResponseWriter, r *http.Request) {
	cat, ok := h.explorerTarget(w, r)
	if !ok {
		return
	}
	target, err := trino_client.QualifiedName(cat.TrinoCatalogName)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid catalog name", err)
		return
	}

	result, err := h.exploreQuery(r, "SHOW SCHEMAS FROM "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list namespaces", err)
		return
	}
	namespaces := make([][]string, 0, len(result.Data))
	for _, row := range result.Data {
		if len(row) > 0 {
			namespaces = append(namespaces, []string{fmt.Sprint(row[0])})
		}
	}
	writeJSON(w, http.StatusOK, namespaces)
}

func (h *Handlers) HandleListTables(w http.ResponseWriter, r *http.Request) {
	cat, ok := h.explorerTarget(w, r)
	if !ok {
		return
	}
	namespace, err := pathParam(r, "namespace")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace", err)
		return
	}
	target, err := trino_client.QualifiedName(cat.TrinoCatalogName, namespace)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace", err)
		return
	}

	result, err := h.exploreQuery(r, "SHOW TABLES FROM "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list tables", err)
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
	writeJSON(w, http.StatusOK, tables)
}

func (h *Handlers) HandleListColumns(w http.ResponseWriter, r *http.Request) {
	cat, ok := h.explorerTarget(w, r)
	if !ok {
		return
	}
	namespace, err := pathParam(r, "namespace")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace", err)
		return
	}
	table, err := pathParam(r, "table")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid table", err)
		return
	}
	target, err := trino_client.QualifiedName(cat.TrinoCatalogName, namespace, table)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace or table", err)
		return
	}

	meta, err := h.exploreQuery(r, "DESCRIBE "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list columns", err)
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
	writeJSON(w, http.StatusOK, cols)
}

func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// pathParam returns a route parameter percent-decoded. chi returns it still
// encoded whenever net/http preserved RawPath (an escape that differs from the
// default encoding, e.g. %24 for $ or %3B for ;), which would send
// "events%24snapshots" to Trino instead of "events$snapshots".
func pathParam(r *http.Request, key string) (string, error) {
	return url.PathUnescape(chi.URLParam(r, key))
}

func uuidStr(u pgtype.UUID) string {
	return openapi_types.UUID(u.Bytes).String()
}

// catalogConnectionEncryptionFailedJSONResponse is the 500 returned when the
// credentials of a connection cannot be sealed on create or update (no key
// configured, or a crypto failure). Nothing is stored.
type catalogConnectionEncryptionFailedJSONResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r catalogConnectionEncryptionFailedJSONResponse) VisitCreateCatalogConnectionResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	return json.NewEncoder(w).Encode(r)
}

func (r catalogConnectionEncryptionFailedJSONResponse) VisitUpdateCatalogConnectionResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	return json.NewEncoder(w).Encode(r)
}

type catalogConnectionConflictJSONResponse struct {
	Message string `json:"message"`
}

func (r catalogConnectionConflictJSONResponse) VisitCreateCatalogConnectionResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	return json.NewEncoder(w).Encode(r)
}
