package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	catalogs "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/catalogs/conns"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListCatalogConnections — GET /catalog-connections (#385). The caller's own
// connections; an admin sees every one, including the ownerless bootstrap
// connection, as authz.Connection allows.
func (h *CatalogHandlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogConnections401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	filter := conns.Filter{WithoutEngine: request.Params.WithoutEngine != nil && *request.Params.WithoutEngine}
	visible, err := h.conns.List(ctx, p.UserID, p.IsAdmin(), filter)
	if err != nil {
		return nil, err
	}

	action := ""
	if request.Params.Action != nil {
		action = *request.Params.Action
	}
	result := make([]CatalogConnection, 0, len(visible))
	for _, c := range visible {
		conn := h.withConnectionActions(ctx, c)
		if action != "" && !connectionAllows(conn, action) {
			continue
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

func (h *CatalogHandlers) CreateCatalogConnection(ctx context.Context, request CreateCatalogConnectionRequestObject) (CreateCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	uid := p.UserID

	// The connection id is generated here, not by the database default: the
	// credentials are sealed with it as additional authenticated data.
	connID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	ev := audit.Event{Action: audit.ConnectionCreate, ResourceType: "connection", ResourceID: idString(connID)}
	if !h.mayManageConnections(p) {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return CreateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only admins may create connections"}}, nil
	}
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	if !requiredName(request.Body.Name) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"name is required",
			FieldError{Field: "name", Code: FieldCodeRequired},
		)}, nil
	}
	cfg, err := h.createConfig(connID, request.Body)
	var ce *configError
	if errors.As(err, &ce) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: ce.response()}, nil
	}
	if err != nil {
		return nil, err
	}
	config, connURL, credsEnc, err := storedColumns(h.core, cfg)
	if err != nil {
		h.logger.ErrorContext(ctx, "encrypt connection credentials", "err", err)
		return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not encrypt the connection credentials"}, nil
	}

	credentialsReadOnly := false
	if request.Body.CredentialsReadOnly != nil {
		credentialsReadOnly = *request.Body.CredentialsReadOnly
	}

	conn, err := h.conns.Create(ctx, db.CreateCatalogConnectionParams{
		ID:                  connID,
		Name:                request.Body.Name,
		Type:                string(cfg.Kind),
		Url:                 connURL,
		Config:              config,
		CredentialsEnc:      credsEnc,
		CredentialsReadOnly: credentialsReadOnly,
		CreatedBy:           uid,
	})
	if errors.Is(err, conns.ErrNameTaken) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return CreateCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "CreateCatalogConnection failed", "err", err)
		return nil, err
	}

	h.catalogCache.Clear()
	ev.Changes = map[string]audit.Change{"name": {To: conn.Name}, "type": {To: conn.Type}, "url": {To: conn.Url}}
	h.audited(ctx, ev.Allowed())
	return CreateCatalogConnection201JSONResponse(h.withConnectionActions(ctx, conn)), nil
}

func (h *CatalogHandlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
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

func (h *CatalogHandlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogConnection401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	connID := pgtype.UUID{Bytes: request.Id, Valid: true}
	ev := audit.Event{Action: audit.ConnectionUpdate, ResourceType: "connection", ResourceID: idString(connID)}
	ctx = audit.WithAction(ctx, ev.Action)
	if !h.mayManageConnections(p) {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only admins may edit connections"}}, nil
	}
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
	params := db.UpdateCatalogConnectionParams{ID: connID, Name: body.Name, CredentialsReadOnly: body.CredentialsReadOnly}
	if body.Url != nil || body.Config != nil || body.Credentials != nil {
		cfg, err := h.updateConfig(stored, body)
		var ce *configError
		switch {
		case errors.As(err, &ce):
			h.audited(ctx, ev.Failed(audit.ReasonValidation))
			return UpdateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: ce.response()}, nil
		case errors.Is(err, catalogs.ErrCredentialsUnreadable):
			h.logger.ErrorContext(ctx, "open connection credentials", "err", err)
			return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not open the stored connection credentials"}, nil
		case err != nil:
			return nil, err
		}
		config, connURL, credsEnc, err := storedColumns(h.core, cfg)
		if err != nil {
			h.logger.ErrorContext(ctx, "encrypt connection credentials", "err", err)
			return catalogConnectionEncryptionFailedJSONResponse{Code: ErrorCodeEncryptionFailed, Message: "could not encrypt the connection credentials"}, nil
		}
		params.Config, params.Url, params.CredentialsEnc = config, &connURL, credsEnc
	}

	conn, err := h.conns.Update(ctx, params)
	if errors.Is(err, conns.ErrNameTaken) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}

	h.catalogCache.Clear()
	ev.Changes = connectionChanges(h.logger, stored, conn, body.Credentials)
	h.audited(ctx, ev.Allowed())
	if body.Url != nil || body.Config != nil || body.Credentials != nil {
		h.resyncConnectionBindings(ctx, p, connID)
	}
	return UpdateCatalogConnection200JSONResponse(h.withConnectionActions(ctx, conn)), nil
}

// resyncTimeout bounds one connection's re-apply, which outlives the request.
const resyncTimeout = 5 * time.Minute

// resyncLocks serializes the re-apply per connection, so two quick edits
// apply in order and each projection reads the latest stored connection. It
// covers one API replica; across replicas the reconciler and a manual sync
// repair a binding left on older settings.
var resyncLocks sync.Map // [16]byte -> *sync.Mutex

// resyncConnectionBindings applies the managed bindings of a connection to
// their engines again after its settings changed (#664). A binding's
// projection is built from the stored connection, so without this the engine
// keeps the catalog it was given before the edit until someone syncs the
// binding by hand. The edit has already succeeded: a binding that cannot be
// applied is left in error with the reason (it shows in Attention), and one
// whose projection cannot be built, or that the editor may not apply, is
// logged and audited. It runs detached from the request, so a client that
// disconnects does not leave bindings half applied.
func (h *CatalogHandlers) resyncConnectionBindings(ctx context.Context, p authz.Principal, connID pgtype.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resyncTimeout)
	defer cancel()
	mu, _ := resyncLocks.LoadOrStore(connID.Bytes, &sync.Mutex{})
	if lock, ok := mu.(*sync.Mutex); ok {
		lock.Lock()
		defer lock.Unlock()
	}

	cats, err := h.queries.ListCatalogsByConnection(ctx, connID)
	if err != nil {
		h.logger.ErrorContext(ctx, "list catalogs to re-apply bindings; sync them by hand", "connection_id", idString(connID), "err", err)
		return
	}
	ctx = audit.WithAction(ctx, audit.BindingSync)
	for _, c := range cats {
		bs, err := h.queries.ListBindingsByCatalog(ctx, c.ID)
		if err != nil {
			h.logger.ErrorContext(ctx, "list bindings to re-apply; sync them by hand", "catalog_id", idString(c.ID), "err", err)
			continue
		}
		for _, b := range bs {
			if b.Mode == "managed" && b.Enabled {
				h.resyncBinding(ctx, p, b.ID)
			}
		}
	}
}

func (h *CatalogHandlers) resyncBinding(ctx context.Context, p authz.Principal, id pgtype.UUID) {
	ev := audit.Event{Action: audit.BindingSync, ResourceType: "binding", ResourceID: idString(id)}
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, id)
	switch {
	case denied != nil:
		// The editor may change the connection but not apply to this engine
		// (connections open to everyone): record it, an engine admin syncs.
		h.logger.WarnContext(ctx, "binding not re-applied: the editor may not apply it to the engine", "binding_id", idString(id))
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return
	case err != nil || invalid != nil:
		h.logger.WarnContext(ctx, "re-apply binding after a connection edit", "binding_id", idString(id), "err", err)
		return
	case !cur.Enabled: // disabled since it was listed (its catalog is being deleted)
		return
	}
	connector := ""
	if cur.Connector != nil {
		connector = *cur.Connector
	}
	proj, err := h.projectBinding(ctx, t.conn, t.engine.ID, t.catalog.ID, h.bindingSpec(t, cur.EngineCatalogName, connector, extraPropsOf(h.logger, cur.ExtraProps)))
	if err != nil {
		h.logger.WarnContext(ctx, "project binding after a connection edit", "binding_id", idString(id), "err", err)
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return
	}
	l := h.lifecycleOn(t.cluster, t.engine)
	synced, err := l.Sync(ctx, cur, proj)
	if err != nil {
		h.logger.WarnContext(ctx, "re-apply binding after a connection edit", "binding_id", idString(id), "err", err)
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return
	}
	h.observe(ctx, l)
	if synced.LifecycleState != bindings.StateActive {
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return
	}
	h.audited(ctx, ev.Allowed())
}

func (h *CatalogHandlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
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

	err = h.conns.Delete(ctx, connID)
	if errors.Is(err, conns.ErrBacksEngine) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConnectionInUse, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}

	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalogConnection204Response{}, nil
}

func (h *CatalogHandlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
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
	ws, err := h.access.PersonalWorkspace(ctx, userID)
	if err != nil {
		h.logger.WarnContext(ctx, "auto-create personal workspace failed", "err", err)
		return nil
	}
	return &ws
}

func dbCatalogConnectionToPublic(logger *slog.Logger, c db.CatalogConnection) CatalogConnection {
	id := openapi_types.UUID(c.ID.Bytes)
	return CatalogConnection{
		Id:                  id,
		Name:                c.Name,
		Type:                c.Type,
		Url:                 redactURLSecrets(c.Url),
		Config:              publicConfig(logger, c.Config),
		HasCredentials:      c.CredentialsEnc != nil && *c.CredentialsEnc != "",
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
func (h *CatalogHandlers) ListWorkspaceCatalogs(w http.ResponseWriter, r *http.Request) {
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
		h.writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "invalid user", nil)
		return
	}
	var wsUUID pgtype.UUID
	if err := wsUUID.Scan(wsID); err != nil || !wsUUID.Valid {
		h.writeAPIError(w, r, http.StatusNotFound, "not_found", "workspace not found", nil)
		return
	}
	// Any member (or a global admin) may list; anyone else is told it does not exist.
	ws, err := h.authz().Workspace(r.Context(), p, wsUUID, authz.ActRead)
	if err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			h.writeAPIError(w, r, http.StatusNotFound, "not_found", "workspace not found", nil)
		} else {
			h.writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "internal error", err)
		}
		return
	}

	var entries []workspaceCatalogEntry
	seen := map[string]bool{}

	// Catalogs of the connections the workspace's own user created (personal workspaces).
	if ws.OwnerUserID.Valid {
		conns, err := h.topology.ConnectionsOf(r.Context(), ws.OwnerUserID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list connections for workspace", "err", err)
			writeJSON(w, http.StatusOK, []workspaceCatalogEntry{})
			return
		}
		for _, conn := range conns {
			// The catalogs table is the source of truth — no Polaris REST call.
			rows, err := h.topology.CatalogsOfConnection(r.Context(), conn.ID)
			if err != nil {
				h.logger.WarnContext(r.Context(), "list catalogs for connection", "conn", conn.Name, "err", err)
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
	granted, err := h.topology.GrantedToWorkspace(r.Context(), wsUUID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list granted catalogs for workspace", "err", err)
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
func (h *core) writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string, cause error) {
	rid := middleware.GetReqID(r.Context())
	if cause != nil {
		h.logger.ErrorContext(r.Context(), "explorer request failed", "code", code, "request_id", rid, "err", cause)
	}
	writeJSON(w, status, map[string]string{"code": code, "message": message, "request_id": rid})
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
