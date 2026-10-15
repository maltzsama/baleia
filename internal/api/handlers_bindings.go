package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// The binding API (#80). Managed bindings are written here and by the catalog
// handlers; the binding a catalog was created with on its Trino cluster's
// engine (its "primary" binding) also keeps the catalog's Trino columns in
// step, which the explorer, Lens and retrievers still read.

func bindingNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: "not_found", Message: "binding not found"}
}

func unprocessable(code, msg string) UnprocessableEntityJSONResponse {
	return UnprocessableEntityJSONResponse{Code: code, Message: msg, Errors: []struct {
		Field   *string `json:"field,omitempty"`
		Message *string `json:"message,omitempty"`
	}{{Message: &msg}}}
}

func extraPropsOf(raw []byte) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// bindingTarget is everything a write on one binding needs.
type bindingTarget struct {
	catalog db.Catalog
	conn    db.CatalogConnection // the catalog's source connection
	engine  db.Engine
	cluster db.TrinoCluster
}

func (t bindingTarget) ref() bindings.EngineRef {
	return bindings.EngineRef{ID: t.engine.ID, ClusterID: t.cluster.ID}
}

// primary reports whether b is the binding the catalog's Trino columns
// describe.
func (t bindingTarget) primary() bool { return t.catalog.ClusterID == t.cluster.ID }

// bindingAccess resolves a catalog and an engine for a binding write: write
// access to both, a source catalog and an engine with a binder.
func (h *Handlers) bindingAccess(ctx context.Context, p authz.Principal, catalogID, engineID pgtype.UUID) (bindingTarget, *notFoundOrForbidden, *UnprocessableEntityJSONResponse, error) {
	cat, conn, err := h.authz().Catalog(ctx, p, catalogID, authz.ActWrite)
	if nf, fb := catalogAccessError(err); nf || fb {
		return bindingTarget{}, &notFoundOrForbidden{notFound: nf, what: "catalog"}, nil, nil
	} else if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	eng, _, err := h.authz().Engine(ctx, p, engineID, authz.ActWrite)
	if nf, fb := catalogAccessError(err); nf || fb {
		return bindingTarget{}, &notFoundOrForbidden{notFound: nf, what: "engine"}, nil, nil
	} else if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	if cat.Origin != "source" {
		u := unprocessable("binding_unsupported", "native catalogs belong to their engine and are not bound")
		return bindingTarget{}, nil, &u, nil
	}
	if eng.Kind != h.binder.EngineKind() {
		u := unprocessable("binding_unsupported", "engine kind "+eng.Kind+" has no binder")
		return bindingTarget{}, nil, &u, nil
	}
	cluster, err := h.queries.GetTrinoClusterByEngine(ctx, eng.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		u := unprocessable("binding_unsupported", "the engine has no Trino cluster")
		return bindingTarget{}, nil, &u, nil
	}
	if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	return bindingTarget{catalog: cat, conn: conn, engine: eng, cluster: cluster}, nil, nil, nil
}

type notFoundOrForbidden struct {
	notFound bool
	what     string
}

func (e *notFoundOrForbidden) notFoundResponse() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: "not_found", Message: e.what + " not found"}
}

func (e *notFoundOrForbidden) forbiddenResponse() ForbiddenJSONResponse {
	return ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to bind this " + e.what}
}

// bindingConflict maps a unique violation of catalog_bindings to a 409.
func bindingConflict(err error) (ConflictJSONResponse, bool) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		return ConflictJSONResponse{}, false
	}
	if pg.ConstraintName == "catalog_bindings_catalog_id_engine_id_key" {
		return ConflictJSONResponse{Code: "binding_exists", Message: "the catalog is already bound to this engine"}, true
	}
	return ConflictJSONResponse{Code: "name_in_use", Message: "the engine already has a catalog of that name"}, true
}

func bindingWithSync(b db.CatalogBinding, version string, syncErr error) CatalogBinding {
	out := bindingToPublic(b)
	status := CatalogBindingSyncStatusSynced
	if syncErr != nil {
		status = CatalogBindingSyncStatusError
		msg := syncErr.Error()
		out.SyncError = &msg
	}
	out.SyncStatus = &status
	if version != "" {
		out.Version = &version
	}
	return out
}

// syncPrimaryCatalog keeps the catalog's Trino columns in step with its
// primary binding.
func syncPrimaryCatalog(ctx context.Context, q db.Querier, t bindingTarget, b db.CatalogBinding) error {
	if !t.primary() {
		return nil
	}
	name := b.EngineCatalogName
	updated, err := q.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID: t.catalog.ID, Name: t.catalog.Name, Description: t.catalog.Description, Warehouse: t.catalog.Warehouse,
		Connector: b.Connector, ExtraProps: b.ExtraProps, TrinoCatalogName: &name,
	})
	if err != nil {
		return err
	}
	if updated.Enabled != b.Enabled {
		return q.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: t.catalog.ID, Enabled: b.Enabled})
	}
	return nil
}

func (h *Handlers) bindingSpec(t bindingTarget, name, connector string, extra map[string]string) bindings.BindingSpec {
	ref := ""
	if t.catalog.SourceRef != nil {
		ref = *t.catalog.SourceRef
	}
	return bindings.BindingSpec{EngineCatalog: name, Connector: connector, SourceRef: ref, ExtraProps: extra}
}

func (h *Handlers) CreateCatalogBinding(ctx context.Context, request CreateCatalogBindingRequestObject) (CreateCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	body := *request.Body
	t, denied, invalid, err := h.bindingAccess(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, pgtype.UUID{Bytes: body.EngineId, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return CreateCatalogBinding404JSONResponse{NotFoundJSONResponse: denied.notFoundResponse()}, nil
	case denied != nil:
		return CreateCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	connector, extra := "", map[string]string{}
	if body.Connector != nil {
		connector = *body.Connector
	}
	if body.ExtraProps != nil {
		extra = *body.ExtraProps
	}
	proj, err := h.projectBinding(t.conn, h.bindingSpec(t, body.EngineCatalogName, connector, extra))
	if bindingInvalid(err) {
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	rawExtra, _ := json.Marshal(extra)

	var created db.CatalogBinding
	txErr := h.inTx(ctx, func(qtx db.Querier) error {
		b, err := qtx.CreateManagedBinding(ctx, db.CreateManagedBindingParams{
			CatalogID: t.catalog.ID, EngineID: t.engine.ID, EngineCatalogName: proj.EngineCatalog,
			Connector: &proj.Connector, ExtraProps: rawExtra,
		})
		if err != nil {
			return err
		}
		created = b
		if err := syncPrimaryCatalog(ctx, qtx, t, b); err != nil {
			return err
		}
		return h.binder.WithStore(qtx).Stage(ctx, t.ref(), proj)
	})
	if c, ok := bindingConflict(txErr); ok {
		return CreateCatalogBinding409JSONResponse{ConflictJSONResponse: c}, nil
	}
	if txErr != nil {
		return nil, fmt.Errorf("create binding: %w", txErr)
	}
	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	applyErr := h.binder.Apply(ddlCtx, proj)
	if applyErr != nil {
		h.markBindingError(ctx, t, proj.EngineCatalog, applyErr)
	}
	h.catalogCache.Clear()
	return CreateCatalogBinding201JSONResponse(bindingWithSync(created, proj.Version, applyErr)), nil
}

func (h *Handlers) markBindingError(ctx context.Context, t bindingTarget, name string, err error) {
	msg := err.Error()
	_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{ClusterID: t.cluster.ID, CatalogName: name, SyncError: &msg})
}

// managedBinding loads a binding for a write: it must exist, be managed and
// the caller must be allowed to write its catalog and engine.
func (h *Handlers) managedBinding(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.CatalogBinding, bindingTarget, *notFoundOrForbidden, *UnprocessableEntityJSONResponse, error) {
	b, err := h.queries.GetBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CatalogBinding{}, bindingTarget{}, &notFoundOrForbidden{notFound: true, what: "binding"}, nil, nil
	}
	if err != nil {
		return db.CatalogBinding{}, bindingTarget{}, nil, nil, err
	}
	t, denied, invalid, err := h.bindingAccess(ctx, p, b.CatalogID, b.EngineID)
	if denied != nil && denied.notFound {
		denied.what = "binding" // never reveal which half was hidden
	}
	if err != nil || denied != nil || invalid != nil {
		return b, t, denied, invalid, err
	}
	if b.Mode != "managed" {
		u := unprocessable("binding_not_managed", "only managed bindings are changed through Baleia; this one is "+b.Mode)
		return b, t, nil, &u, nil
	}
	return b, t, nil, nil, nil
}

func (h *Handlers) UpdateCatalogBinding(ctx context.Context, request UpdateCatalogBindingRequestObject) (UpdateCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return UpdateCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return UpdateCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if request.Body == nil {
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	body := *request.Body
	name, extra, enabled := cur.EngineCatalogName, extraPropsOf(cur.ExtraProps), cur.Enabled
	if body.EngineCatalogName != nil {
		name = *body.EngineCatalogName
	}
	if body.ExtraProps != nil {
		extra = *body.ExtraProps
	}
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	connector := ""
	if cur.Connector != nil {
		connector = *cur.Connector
	}
	proj, err := h.projectBinding(t.conn, h.bindingSpec(t, name, connector, extra))
	if bindingInvalid(err) {
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	rawExtra, _ := json.Marshal(extra)

	var updated db.CatalogBinding
	txErr := h.inTx(ctx, func(qtx db.Querier) error {
		b, err := qtx.UpdateBinding(ctx, db.UpdateBindingParams{
			ID: cur.ID, EngineCatalogName: name, Connector: &proj.Connector, ExtraProps: rawExtra, Enabled: enabled,
		})
		if err != nil {
			return err
		}
		updated = b
		return syncPrimaryCatalog(ctx, qtx, t, b)
	})
	if c, ok := bindingConflict(txErr); ok {
		return UpdateCatalogBinding409JSONResponse{ConflictJSONResponse: c}, nil
	}
	if txErr != nil {
		return nil, fmt.Errorf("update binding: %w", txErr)
	}

	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	b := h.binder.WithStore(h.queries)
	var applyErr error
	switch {
	case !enabled && cur.Enabled:
		applyErr = b.Drop(ddlCtx, t.ref(), cur.EngineCatalogName)
	case !enabled:
		// stays disabled: nothing on the engine
	case !cur.Enabled:
		applyErr = b.Create(ddlCtx, t.ref(), proj)
	case name != cur.EngineCatalogName:
		applyErr = b.Rename(ddlCtx, t.ref(), cur.EngineCatalogName, proj)
	default:
		applyErr = b.Replace(ddlCtx, t.ref(), proj)
	}
	if applyErr != nil && enabled {
		h.markBindingError(ctx, t, name, applyErr)
	}
	h.catalogCache.Clear()
	version := proj.Version
	if !enabled {
		version = ""
	}
	return UpdateCatalogBinding200JSONResponse(bindingWithSync(updated, version, applyErr)), nil
}

func (h *Handlers) DeleteCatalogBinding(ctx context.Context, request DeleteCatalogBindingRequestObject) (DeleteCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return DeleteCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return DeleteCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		return DeleteCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if t.primary() {
		return DeleteCatalogBinding409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: "binding_is_primary", Message: "this is the catalog's own binding on its Trino cluster; delete the catalog instead",
		}}, nil
	}
	if err := h.inTx(ctx, func(qtx db.Querier) error {
		if err := qtx.DeleteBinding(ctx, cur.ID); err != nil {
			return err
		}
		return h.binder.WithStore(qtx).Unstage(ctx, t.ref(), cur.EngineCatalogName)
	}); err != nil {
		return nil, err
	}
	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if cur.Enabled {
		if err := h.binder.Remove(ddlCtx, cur.EngineCatalogName); err != nil {
			// The binding is gone; the catalog may still be live in Trino and
			// shows up as orphan_in_trino in /trino/reconcile.
			slog.Warn("drop catalog of deleted binding", "catalog", cur.EngineCatalogName, "err", err)
		}
	}
	h.catalogCache.Clear()
	return DeleteCatalogBinding204Response{}, nil
}

func (h *Handlers) SyncCatalogBinding(ctx context.Context, request SyncCatalogBindingRequestObject) (SyncCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SyncCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return SyncCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return SyncCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if !cur.Enabled {
		return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("binding_disabled", "enable the binding to apply it")}, nil
	}
	connector := ""
	if cur.Connector != nil {
		connector = *cur.Connector
	}
	proj, err := h.projectBinding(t.conn, h.bindingSpec(t, cur.EngineCatalogName, connector, extraPropsOf(cur.ExtraProps)))
	if err != nil {
		h.markBindingError(ctx, t, cur.EngineCatalogName, err)
		if bindingInvalid(err) {
			return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
		}
		return SyncCatalogBinding409JSONResponse(bindingWithSync(cur, "", err)), nil
	}
	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.binder.WithStore(h.queries).Replace(ddlCtx, t.ref(), proj); err != nil {
		h.markBindingError(ctx, t, cur.EngineCatalogName, err)
		return SyncCatalogBinding409JSONResponse(bindingWithSync(cur, "", err)), nil
	}
	return SyncCatalogBinding200JSONResponse(bindingWithSync(cur, proj.Version, nil)), nil
}

func (h *Handlers) GetEngineBindingSupport(ctx context.Context, request GetEngineBindingSupportRequestObject) (GetEngineBindingSupportResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineBindingSupport401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	eng, _, err := h.authz().Engine(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return GetEngineBindingSupport404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	out := GetEngineBindingSupport200JSONResponse{}
	if h.binder == nil || eng.Kind != h.binder.EngineKind() {
		return out, nil
	}
	protected, reserved := h.binder.ProtectedKeyPatterns(), h.binder.ReservedNames()
	kinds := make([]connections.Kind, 0)
	for k := range h.binder.Supports() {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for _, k := range kinds {
		out[string(k)] = BindingSupport{Connectors: h.binder.Supports()[k], ProtectedKeyPatterns: protected, ReservedNames: reserved}
	}
	return out, nil
}
