package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// engineNameRe mirrors the engines.name CHECK.
var engineNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// enginePingTimeout bounds the reachability check of a new engine.
const enginePingTimeout = 15 * time.Second

// inTx runs fn in one transaction. Without a pool (unit tests) it runs fn on
// the handler's querier directly.
func (h *Handlers) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	if h.pool == nil {
		return fn(h.queries)
	}
	return withTx(ctx, h.pool, func(tx pgx.Tx) error { return fn(db.New(tx)) })
}

// isFKViolation reports a delete blocked by a foreign key: 23503 for NO
// ACTION, 23001 for ON DELETE RESTRICT (engines use RESTRICT).
func isFKViolation(e *pgconn.PgError) bool {
	return e.Code == "23503" || e.Code == "23001"
}

func engineToPublic(e db.Engine) Engine {
	out := Engine{
		Id:           openapi_types.UUID(e.ID.Bytes),
		Name:         e.Name,
		Kind:         e.Kind,
		Class:        EngineClass(e.Class),
		ConnectionId: openapi_types.UUID(e.ConnectionID.Bytes),
		IdentityMode: EngineIdentityMode(e.IdentityMode),
		Enabled:      e.Enabled,
		CreatedAt:    pgTimestampToTime(e.CreatedAt),
	}
	out.UpdatedAt = pgTimestampToTimePtr(e.UpdatedAt)
	return out
}

func bindingToPublic(b db.CatalogBinding) CatalogBinding {
	return CatalogBinding{
		Id:                openapi_types.UUID(b.ID.Bytes),
		CatalogId:         openapi_types.UUID(b.CatalogID.Bytes),
		EngineId:          openapi_types.UUID(b.EngineID.Bytes),
		EngineCatalogName: b.EngineCatalogName,
		Connector:         b.Connector,
		Mode:              CatalogBindingMode(b.Mode),
		Enabled:           b.Enabled,
		ExtraProps:        ptrExtraProps(b.ExtraProps),
		LifecycleState:    ptrLifecycle(b.LifecycleState),
		LastErrorCode:     b.LastErrorCode,
		LastErrorMessage:  b.LastErrorMessage,
	}
}

func ptrExtraProps(raw []byte) *map[string]string {
	m := extraPropsOf(raw)
	return &m
}

func ptrLifecycle(s string) *CatalogBindingLifecycleState {
	if s == "" {
		return nil
	}
	v := CatalogBindingLifecycleState(s)
	return &v
}

func engineNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}
}

// ListEngines returns every engine to an admin, else the engines on the
// caller's connections and those hosting a catalog granted to them.
func (h *Handlers) ListEngines(ctx context.Context, _ ListEnginesRequestObject) (ListEnginesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListEngines401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	var engines []db.Engine
	if p.IsAdmin() {
		engines, err = h.queries.ListEngines(ctx)
	} else {
		engines, err = h.queries.ListEnginesVisibleToUser(ctx, p.UserID)
	}
	if err != nil {
		return nil, err
	}
	return ListEngines200JSONResponse(h.describeEngines(ctx, engines)), nil
}

func (h *Handlers) GetEngine(ctx context.Context, request GetEngineRequestObject) (GetEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	eng, _, err := h.authz().Engine(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return GetEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	return GetEngine200JSONResponse(h.describeEngine(ctx, eng)), nil
}

// CreateEngine registers a direct engine on a connection the caller owns (or
// any connection, for an admin), after checking Baleia can reach it, together
// with the native catalogs it exposes.
func (h *Handlers) CreateEngine(ctx context.Context, request CreateEngineRequestObject) (CreateEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	body := request.Body
	invalid := func(code ErrorCode, field, msg string) CreateEngineResponseObject {
		return CreateEngine422JSONResponse{UnprocessableEntityJSONResponse: (&configError{code: code, fields: []connections.FieldError{{Key: field, Message: msg}}}).response()}
	}
	if !engineNameRe.MatchString(body.Name) {
		return invalid("validation_error", "name", "must match "+engineNameRe.String()), nil
	}

	conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: body.ConnectionId, Valid: true}, authz.ActWrite)
	if errors.Is(err, authz.ErrNotFound) {
		return CreateEngine404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog connection not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	provider, cfg, err := h.connectionConfig(conn)
	switch {
	case errors.Is(err, connections.ErrUnknownKind):
		return invalid("engine_unsupported", "connection_id", fmt.Sprintf("connections of type %q cannot be engines", conn.Type)), nil
	case err != nil:
		return nil, err
	}
	if !connections.HasRole(provider, connections.RoleDirect) {
		return invalid("engine_unsupported", "connection_id", fmt.Sprintf("connections of type %q cannot be direct engines", conn.Type)), nil
	}

	pingCtx, cancel := context.WithTimeout(ctx, enginePingTimeout)
	defer cancel()
	if err := provider.Ping(pingCtx, cfg); err != nil {
		slog.Info("engine reachability check failed", "connection", conn.Name, "err", err)
		return invalid("engine_unreachable", "connection_id", connectionTestError(err)), nil
	}
	natives, err := catalogs.NativeNames(pingCtx, provider, cfg, conn.Name)
	if err != nil {
		return invalid("engine_unreachable", "connection_id", connectionTestError(err)), nil
	}

	var created db.Engine
	var nativeIDs []pgtype.UUID
	err = h.inTx(ctx, func(q db.Querier) error {
		eng, err := q.CreateEngine(ctx, db.CreateEngineParams{
			Name:         body.Name,
			Kind:         provider.EngineKind(),
			Class:        "direct",
			ConnectionID: conn.ID,
			IdentityMode: "connection", // v1: the connection's credentials (docs/adr/0011)
			CreatedBy:    p.UserID,
		})
		if err != nil {
			return err
		}
		created = eng
		for _, name := range natives {
			cat, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: conn.ID, Name: name})
			if err != nil {
				return err
			}
			nativeIDs = append(nativeIDs, cat.ID)
			if _, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{CatalogID: cat.ID, EngineID: eng.ID, EngineCatalogName: name}); err != nil {
				return err
			}
		}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		msg := "an engine with this name already exists"
		if pgErr.ConstraintName == "engines_connection_id_key" {
			msg = "this connection already backs an engine"
		}
		return CreateEngine409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: msg}}, nil
	}
	if err != nil {
		return nil, err
	}
	for _, id := range nativeIDs {
		h.startMetadataSync(ctx, id)
	}
	return CreateEngine201JSONResponse(h.withEngineActions(ctx, created)), nil
}

func (h *Handlers) UpdateEngine(ctx context.Context, request UpdateEngineRequestObject) (UpdateEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	_, _, err = h.authz().Engine(ctx, p, id, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return UpdateEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return UpdateEngine403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the connection owner or an admin may change this engine"}}, nil
	case err != nil:
		return nil, err
	}
	eng, err := h.queries.UpdateEngine(ctx, db.UpdateEngineParams{ID: id, Enabled: request.Body.Enabled})
	if err != nil {
		return nil, err
	}
	return UpdateEngine200JSONResponse(h.withEngineActions(ctx, eng)), nil
}

// DeleteEngine removes an engine with its native catalogs and bindings. It is
// refused while managed bindings or running executions exist.
func (h *Handlers) DeleteEngine(ctx context.Context, request DeleteEngineRequestObject) (DeleteEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	_, _, err = h.authz().Engine(ctx, p, id, authz.ActAdmin)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DeleteEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DeleteEngine403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the connection owner or an admin may delete this engine"}}, nil
	case err != nil:
		return nil, err
	}

	inUse := func(msg string) DeleteEngineResponseObject {
		return DeleteEngine409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeEngineInUse, Message: msg}}
	}
	blockers, err := h.queries.CountEngineBlockers(ctx, id)
	if err != nil {
		return nil, err
	}
	if blockers.ManagedBindings > 0 || blockers.RunningExecutions > 0 {
		return inUse(fmt.Sprintf("the engine has %d managed bindings and %d running executions", blockers.ManagedBindings, blockers.RunningExecutions)), nil
	}

	err = h.inTx(ctx, func(q db.Querier) error {
		if err := q.DeleteNativeCatalogsOfEngine(ctx, id); err != nil {
			return err
		}
		return q.DeleteEngine(ctx, id)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && isFKViolation(pgErr) {
		// Still referenced, e.g. by a Trino cluster (the plugin's table).
		return inUse("the engine is still referenced"), nil
	}
	if err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	return DeleteEngine204Response{}, nil
}

func (h *Handlers) ListEngineBindings(ctx context.Context, request ListEngineBindingsRequestObject) (ListEngineBindingsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListEngineBindings401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	_, conn, err := h.authz().Engine(ctx, p, id, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return ListEngineBindings404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	bindings, err := h.queries.ListBindingsByEngine(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogBinding, 0, len(bindings))
	full := p.IsAdmin() || (conn.CreatedBy.Valid && conn.CreatedBy.Bytes == p.UserID.Bytes)
	for _, b := range bindings {
		// A grantee sees only the bindings of catalogs granted to them.
		if !full {
			if _, _, err := h.authz().Catalog(ctx, p, b.CatalogID, authz.ActRead); err != nil {
				if errors.Is(err, authz.ErrNotFound) {
					continue
				}
				return nil, err
			}
		}
		out = append(out, h.bindingResponse(ctx, b, ""))
	}
	return ListEngineBindings200JSONResponse(out), nil
}

func (h *Handlers) ListCatalogBindings(ctx context.Context, request ListCatalogBindingsRequestObject) (ListCatalogBindingsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogBindings401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			return ListCatalogBindings404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		}
		return nil, err
	}
	bindings, err := h.queries.ListBindingsByCatalog(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, h.bindingResponse(ctx, b, ""))
	}
	return ListCatalogBindings200JSONResponse(out), nil
}
