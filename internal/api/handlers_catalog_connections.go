package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	catalogs "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListCatalogConnections — GET /catalog-connections (#385). The caller's own
// connections; an admin sees every one, including the ownerless bootstrap
// connection, as authz.Connection allows.
func (h *Handlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogConnections401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	conns, err := h.queries.ListVisibleCatalogConnections(ctx, db.ListVisibleCatalogConnectionsParams{IsAdmin: p.IsAdmin(), UserID: p.UserID})
	if err != nil {
		return nil, err
	}

	action := ""
	if request.Params.Action != nil {
		action = *request.Params.Action
	}
	withoutEngine := request.Params.WithoutEngine != nil && *request.Params.WithoutEngine
	var engineConnections map[[16]byte]struct{}
	if withoutEngine {
		engines, err := h.queries.ListEngines(ctx)
		if err != nil {
			return nil, err
		}
		engineConnections = make(map[[16]byte]struct{}, len(engines))
		for _, e := range engines {
			engineConnections[e.ConnectionID.Bytes] = struct{}{}
		}
	}

	result := make([]CatalogConnection, 0, len(conns))
	for _, c := range conns {
		conn := h.withConnectionActions(ctx, c)
		if action != "" && !connectionAllows(conn, action) {
			continue
		}
		if withoutEngine {
			if _, used := engineConnections[c.ID.Bytes]; used {
				continue
			}
		}
		result = append(result, conn)
	}
	return ListCatalogConnections200JSONResponse(result), nil
}

// connectionAllows reports whether the computed allowed_actions include the
// requested action (#110).
func connectionAllows(conn CatalogConnection, action string) bool {
	if conn.AllowedActions == nil {
		return false
	}
	for _, allowed := range *conn.AllowedActions {
		if string(allowed) == action {
			return true
		}
	}
	return false
}

func (h *Handlers) CreateCatalogConnection(ctx context.Context, request CreateCatalogConnectionRequestObject) (CreateCatalogConnectionResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	// The connection id is generated here, not by the database default: the
	// credentials are sealed with it as additional authenticated data.
	connID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	ev := audit.Event{Action: audit.ConnectionCreate, ResourceType: "connection", ResourceID: idString(connID)}
	cfg, err := h.createConfig(connID, request.Body)
	var ce *configError
	if errors.As(err, &ce) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: ce.response()}, nil
	}
	if err != nil {
		return nil, err
	}
	config, connURL, credsEnc, err := storedColumns(h, cfg)
	if err != nil {
		slog.Error("encrypt connection credentials", "err", err)
		return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not encrypt the connection credentials"}, nil
	}

	defaultReadonly := true
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}
	credentialsReadOnly := false
	if request.Body.CredentialsReadOnly != nil {
		credentialsReadOnly = *request.Body.CredentialsReadOnly
	}

	conn, err := h.queries.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID:                  connID,
		Name:                request.Body.Name,
		Type:                string(cfg.Kind),
		Url:                 connURL,
		Config:              config,
		CredentialsEnc:      credsEnc,
		DefaultReadonly:     defaultReadonly,
		CredentialsReadOnly: credentialsReadOnly,
		CreatedBy:           uid,
	})
	if err != nil {
		slog.Error("CreateCatalogConnection failed", "err", err)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			h.audited(ctx, ev.Failed(audit.ReasonConflict))
			return catalogConnectionConflictJSONResponse{Message: "a connection with this name already exists"}, nil
		}
		return nil, err
	}

	h.catalogCache.Clear()
	ev.Changes = map[string]audit.Change{"name": {To: conn.Name}, "type": {To: conn.Type}, "url": {To: conn.Url}}
	h.audited(ctx, ev.Allowed())
	return CreateCatalogConnection201JSONResponse(h.withConnectionActions(ctx, conn)), nil
}

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	// Owner or global admin only; anyone else gets 404 so existence is not
	// revealed.
	conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return GetCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	// Never return the stored secret. Callers see only has_credentials.
	return GetCatalogConnection200JSONResponse(h.withConnectionActions(ctx, conn)), nil
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	connID := pgtype.UUID{Bytes: request.Id, Valid: true}
	ev := audit.Event{Action: audit.ConnectionUpdate, ResourceType: "connection", ResourceID: idString(connID)}
	ctx = audit.WithAction(ctx, ev.Action)
	stored, err := h.authz().Connection(ctx, p, connID, authz.ActWrite)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return UpdateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	// Only fields present in the request are written; the SQL COALESCE leaves
	// the others untouched, so concurrent PATCHes to different fields do not
	// overwrite each other.
	body := *request.Body
	params := db.UpdateCatalogConnectionParams{ID: connID, Name: body.Name, DefaultReadonly: body.DefaultReadonly, CredentialsReadOnly: body.CredentialsReadOnly}
	if body.Url != nil || body.Config != nil || body.Credentials != nil {
		cfg, err := h.updateConfig(stored, body)
		var ce *configError
		switch {
		case errors.As(err, &ce):
			h.audited(ctx, ev.Failed(audit.ReasonValidation))
			return UpdateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: ce.response()}, nil
		case errors.Is(err, catalogs.ErrCredentialsUnreadable):
			slog.Error("open connection credentials", "err", err)
			return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not open the stored connection credentials"}, nil
		case err != nil:
			return nil, err
		}
		config, connURL, credsEnc, err := storedColumns(h, cfg)
		if err != nil {
			slog.Error("encrypt connection credentials", "err", err)
			return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not encrypt the connection credentials"}, nil
		}
		params.Config, params.Url, params.CredentialsEnc = config, &connURL, credsEnc
	}

	conn, err := h.queries.UpdateCatalogConnection(ctx, params)
	if err != nil {
		return nil, err
	}

	h.catalogCache.Clear()
	ev.Changes = connectionChanges(stored, conn, body.Credentials)
	h.audited(ctx, ev.Allowed())
	return UpdateCatalogConnection200JSONResponse(h.withConnectionActions(ctx, conn)), nil
}

func (h *Handlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	connID := pgtype.UUID{Bytes: request.Id, Valid: true}
	ev := audit.Event{Action: audit.ConnectionDelete, ResourceType: "connection", ResourceID: idString(connID)}
	ctx = audit.WithAction(ctx, ev.Action)
	if _, err := h.authz().Connection(ctx, p, connID, authz.ActAdmin); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return DeleteCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	if err := h.queries.DeleteCatalogConnection(ctx, connID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && isFKViolation(pgErr) && pgErr.ConstraintName == "engines_connection_id_fkey" {
			h.audited(ctx, ev.Failed(audit.ReasonConflict))
			return DeleteCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConnectionInUse, Message: "an engine runs on this connection; delete the engine first"}}, nil
		}
		return nil, err
	}

	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalogConnection204Response{}, nil
}

func (h *Handlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return TestCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	// Only the owner (or a global admin) may trigger a probe with the stored
	// credentials; checked before any outbound call.
	ev := audit.Event{Action: audit.ConnectionTest, ResourceType: "connection", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	ctx = audit.WithAction(ctx, ev.Action)
	conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActWrite)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return TestCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
		}
		return nil, err
	}

	provider, cfg, err := h.connectionConfig(conn)
	if err != nil {
		code := classifyTestError(err)
		msg := codeMessages[code]
		ec := TestCatalogConnectionResultErrorCode(code)
		h.audited(ctx, testOutcome(ev, false))
		return TestCatalogConnection200JSONResponse{Success: false, Error: ptr(code + ": " + msg), ErrorCode: &ec, ErrorMessage: &msg}, nil
	}
	t, _, err := h.planTest(ctx, p, provider, cfg, conn.Name, nil)
	if err != nil {
		return nil, err
	}
	res := t.run(ctx)
	if res.Success {
		res.Version = &conn.Type
	}
	h.audited(ctx, testOutcome(ev, res.Success))
	return TestCatalogConnection200JSONResponse(res), nil
}

// connectionConfig returns the provider of a stored connection and its
// decrypted config.
func (h *core) connectionConfig(conn db.CatalogConnection) (connections.Provider, connections.Config, error) {
	plain, err := catalogs.OpenCredentials(conn, h.keyring)
	if err != nil {
		return nil, connections.Config{}, err
	}
	kind := connections.Kind(conn.Type)
	provider, err := h.connections.Get(kind)
	if err != nil {
		return nil, connections.Config{}, err
	}
	cfg, err := providers.FromStored(h.connections, kind, conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return nil, connections.Config{}, err
	}
	return provider, cfg, nil
}

// connectionTestError is the message a failed connection test returns. A
// dialer refusal gets a fixed message so the resolved address is never echoed.
func connectionTestError(err error) string {
	switch {
	case errors.Is(err, connections.ErrDeniedNetwork):
		return "connection_denied_network: the destination is not an allowed network address"
	case errors.Is(err, connections.ErrUnknownKind):
		return "provider_unsupported: this connection type cannot be tested yet"
	}
	return err.Error()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *core) ensurePersonalWorkspace(ctx context.Context, userID pgtype.UUID) *db.Workspace {
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
		Id:                  id,
		Name:                c.Name,
		Type:                c.Type,
		Url:                 redactURLSecrets(c.Url),
		Config:              publicConfig(c.Config),
		HasCredentials:      c.CredentialsEnc != nil && *c.CredentialsEnc != "",
		DefaultReadonly:     c.DefaultReadonly,
		CredentialsReadOnly: c.CredentialsReadOnly,
		CreatedAt:           pgTimestampToTime(c.CreatedAt),
		UpdatedAt:           func() *time.Time { t := pgTimestampToTime(c.UpdatedAt); return &t }(),
	}
}

// Catalog handlers (ListCatalogs, CreateCatalog, GetCatalog, UpdateCatalog,
// DeleteCatalog, SyncCatalog, ReconcileTrino) and the write-through flow
// (Project + UpsertRegistry + DDL) live in handlers_catalogs_write.go.

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

	p, err := authz.PrincipalFromContext(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "invalid user", nil)
		return
	}
	var wsUUID pgtype.UUID
	if err := wsUUID.Scan(wsID); err != nil || !wsUUID.Valid {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "workspace not found", nil)
		return
	}
	// Any member (or a global admin) may list; anyone else is told it does not exist.
	ws, err := h.authz().Workspace(r.Context(), p, wsUUID, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "workspace not found", nil)
		} else {
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "internal error", err)
		}
		return
	}

	var entries []workspaceCatalogEntry
	seen := map[string]bool{}

	// Catalogs of the connections the workspace's own user created (personal workspaces).
	if ws.OwnerUserID.Valid {
		conns, err := h.queries.ListCatalogConnections(r.Context(), ws.OwnerUserID)
		if err != nil {
			slog.Error("list connections for workspace", "err", err)
			writeJSON(w, http.StatusOK, []workspaceCatalogEntry{})
			return
		}
		for _, conn := range conns {
			// The catalogs table is the source of truth — no Polaris REST call.
			rows, err := h.queries.ListCatalogsByConnection(r.Context(), conn.ID)
			if err != nil {
				slog.Warn("list catalogs for connection", "conn", conn.Name, "err", err)
				continue
			}
			for _, row := range rows {
				// The tree lists Trino catalogs; native catalogs of direct
				// engines are reached through targets (#13 PR 2b, #24).
				name := row.TrinoName()
				if !row.Enabled || name == "" || seen[name] {
					continue
				}
				seen[name] = true
				entries = append(entries, workspaceCatalogEntry{
					ConnectionID:   uuidStr(conn.ID),
					ConnectionName: conn.Name,
					ConnectionType: conn.Type,
					CatalogName:    name,
				})
			}
		}
	}

	// Catalogs granted to the workspace, whoever owns their connection.
	granted, err := h.queries.ListGrantedCatalogsOfWorkspace(r.Context(), wsUUID)
	if err != nil {
		slog.Error("list granted catalogs for workspace", "err", err)
		writeJSON(w, http.StatusOK, []workspaceCatalogEntry{})
		return
	}
	for _, row := range granted {
		if row.TrinoCatalogName == nil || seen[*row.TrinoCatalogName] {
			continue
		}
		name := *row.TrinoCatalogName
		seen[name] = true
		entries = append(entries, workspaceCatalogEntry{
			ConnectionID:   uuidStr(row.ConnectionID),
			ConnectionName: row.ConnectionName,
			ConnectionType: row.ConnectionType,
			CatalogName:    name,
		})
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
func (h *core) explorerTarget(w http.ResponseWriter, r *http.Request) (db.Catalog, bool) {
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

// exploreMaxRows bounds one explorer listing.
const exploreMaxRows = 100000

// exploreEngine returns the engine explorer statements run on (the default
// Trino engine until targets reach the explorer, #23).
func (h *core) exploreEngine(r *http.Request) (queryengine.Engine, error) {
	return h.engines.DefaultTrino(r.Context())
}

// exploreQuery runs one metadata statement as the caller's Trino identity
// under the request context with a 30 s deadline, so a client disconnect
// cancels it.
func (h *core) exploreQuery(r *http.Request, eng queryengine.Engine, sql string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	spec := queryengine.SessionSpec{Tags: queryengine.Tags{Source: "explorer", RequestID: middleware.GetReqID(ctx)}}
	if p, err := authz.PrincipalFromContext(ctx); err == nil {
		identity, err := h.trinoIdentityFor(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		spec.Identity = queryengine.Identity{UserID: identity}
	}
	return queryengine.CollectStrings(ctx, eng, spec, sql, exploreMaxRows)
}

func (h *Handlers) HandleListNamespaces(w http.ResponseWriter, r *http.Request) {
	cat, ok := h.explorerTarget(w, r)
	if !ok {
		return
	}
	eng, err := h.exploreEngine(r)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list namespaces", err)
		return
	}
	target, err := eng.Dialect().QualifiedName(cat.TrinoName())
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid catalog name", err)
		return
	}

	rows, err := h.exploreQuery(r, eng, "SHOW SCHEMAS FROM "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list namespaces", err)
		return
	}
	namespaces := make([][]string, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 {
			namespaces = append(namespaces, []string{row[0]})
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
	eng, err := h.exploreEngine(r)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list tables", err)
		return
	}
	target, err := eng.Dialect().QualifiedName(cat.TrinoName(), namespace)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace", err)
		return
	}

	rows, err := h.exploreQuery(r, eng, "SHOW TABLES FROM "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list tables", err)
		return
	}
	type tableEntry struct {
		Name      *string   `json:"name,omitempty"`
		Namespace *[]string `json:"namespace,omitempty"`
	}
	tables := make([]tableEntry, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 {
			name := row[0]
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
	eng, err := h.exploreEngine(r)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list columns", err)
		return
	}
	target, err := eng.Dialect().QualifiedName(cat.TrinoName(), namespace, table)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_identifier", "invalid namespace or table", err)
		return
	}

	meta, err := h.exploreQuery(r, eng, "DESCRIBE "+target)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "engine_error", "could not list columns", err)
		return
	}
	type colEntry struct {
		Name *string `json:"name,omitempty"`
		Type *string `json:"type,omitempty"`
	}
	cols := make([]colEntry, 0, len(meta))
	for _, row := range meta {
		if len(row) >= 2 {
			name := row[0]
			typ := row[1]
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
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
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
