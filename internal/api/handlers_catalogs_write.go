package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/catalogs"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ─────────────────────────────────────────────────────────────────────────
// Catalog handlers (CR-017 §I7): write-through flow to catalogs +
// trino_catalog_registry + Trino DDL (with @baleia-secret[...] placeholder).
//
// Source of truth for "which catalogs exist" is the catalogs table in
// PostgreSQL. The baleia-trino-catalog-store plugin loads them into Trino
// at boot. No more Polaris-REST dynamic discovery (L1).
// ─────────────────────────────────────────────────────────────────────────

// catalogDDLTimeout bounds every catalog DDL sent to Trino, so a stuck
// CREATE/DROP CATALOG surfaces as a sync error instead of blocking the request.
var catalogDDLTimeout = 30 * time.Second

func (h *Handlers) authz() *authz.Checker {
	c := authz.New(h.queries)
	if h.metrics.authz != nil {
		c.WithDenials(h.metrics.authz.Denied)
	}
	// A refused write is audited once, here, under the handler's action
	// (audit.WithAction); refused reads are not (#43).
	c.OnDeny(func(ctx context.Context, resource, id, reason string) {
		if action, ok := audit.ActionFrom(ctx); ok {
			h.audited(ctx, audit.Event{Action: action, ResourceType: resource, ResourceID: id, Outcome: audit.Denied, Reason: reason})
		}
	})
	return c
}

// ListCatalogs — GET /connections/{connection_id}/catalogs
func (h *Handlers) ListCatalogs(ctx context.Context, request ListCatalogsRequestObject) (ListCatalogsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogs401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	if _, err := h.authz().Connection(ctx, p, connID, authz.ActRead); err != nil {
		if !errors.Is(err, authz.ErrNotFound) {
			return nil, err
		}
		// Not the connection's owner: a member of a workspace it was granted to sees
		// only the granted catalogs of this connection, never their siblings.
		granted, gerr := h.queries.ListCatalogsGrantedToUser(ctx, db.ListCatalogsGrantedToUserParams{ConnectionID: connID, UserID: p.UserID})
		if gerr != nil {
			return nil, gerr
		}
		if len(granted) == 0 {
			return ListCatalogs404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		out := make([]Catalog, 0, len(granted))
		for _, r := range granted {
			out = append(out, h.catalogWithStatus(ctx, r))
		}
		return ListCatalogs200JSONResponse(out), nil
	}
	rows, err := h.queries.ListCatalogsByConnection(ctx, connID)
	if err != nil {
		slog.Error("list catalogs by connection", "err", err)
		return ListCatalogs200JSONResponse([]Catalog{}), nil
	}
	out := make([]Catalog, 0, len(rows))
	for _, r := range rows {
		out = append(out, h.catalogWithStatus(ctx, r))
	}
	return ListCatalogs200JSONResponse(out), nil
}

// CreateCatalog — POST /connections/{connection_id}/catalogs
// Write-through flow (CR-017 §I7b) through the Trino binder (#80): the
// catalog row and the registry row are written in one transaction, then
// CREATE CATALOG is sent.
func (h *Handlers) CreateCatalog(ctx context.Context, request CreateCatalogRequestObject) (CreateCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	ev := audit.Event{Action: audit.CatalogCreate, ResourceType: "catalog"}
	ctx = audit.WithAction(ctx, ev.Action)
	conn, err := h.authz().Connection(ctx, p, connID, authz.ActWrite)
	if err != nil {
		switch {
		case errors.Is(err, authz.ErrNotFound):
			return CreateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		case errors.Is(err, authz.ErrForbidden):
			return CreateCatalog403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to create catalogs on this connection"}}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalog422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: "validation_error", Message: "missing body"}}, nil
	}
	body := *request.Body
	if body.TrinoCatalogName == "" || body.Name == "" || body.Warehouse == "" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalog422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: "validation_error", Message: "trino_catalog_name, name and warehouse are required"}}, nil
	}

	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("get default cluster: %w", err)
	}
	connector := ""
	if body.Connector != nil {
		connector = *body.Connector
	}
	extra := map[string]string{}
	if body.ExtraProps != nil {
		extra = *body.ExtraProps
	}
	spec := bindings.BindingSpec{EngineCatalog: body.TrinoCatalogName, Connector: connector, SourceRef: body.Warehouse, ExtraProps: extra}
	proj, err := h.projectBinding(conn, spec)
	if bindingInvalid(err) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalog422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	extraProps, _ := json.Marshal(extra)
	if !cluster.EngineID.Valid {
		return nil, errors.New("the default Trino cluster has no engine")
	}
	eng, err := h.queries.GetEngineByID(ctx, cluster.EngineID)
	if err != nil {
		return nil, err
	}

	// Desired state first (catalog and its binding, provisioning), then the
	// lifecycle applies it (#38).
	var cat db.Catalog
	var binding db.CatalogBinding
	txErr := h.inTx(ctx, func(q db.Querier) error {
		c, err := q.CreateCatalog(ctx, db.CreateCatalogParams{
			ConnectionID:     connID,
			Name:             body.Name,
			Description:      body.Description,
			TrinoCatalogName: &body.TrinoCatalogName,
			ClusterID:        cluster.ID,
			Warehouse:        &body.Warehouse,
			Connector:        &proj.Connector,
			ExtraProps:       extraProps,
		})
		if err != nil {
			return err
		}
		cat = c
		binding, err = upsertPrimaryBinding(ctx, q, cluster, c, bindings.StateProvisioning, proj.Version)
		return err
	})
	if txErr != nil {
		return nil, fmt.Errorf("create catalog: %w", txErr)
	}

	l := h.lifecycleOn(cluster, eng)
	binding, err = l.Provision(ctx, binding, proj)
	if errors.Is(err, bindings.ErrConcurrentChange) {
		h.audited(ctx, withID(ev, cat.ID).Failed(audit.ReasonConflict))
		return CreateCatalog409JSONResponse{ConflictJSONResponse: concurrentChange(binding)}, nil
	}
	if err != nil {
		return nil, err
	}
	observe(ctx, l)
	h.catalogCache.Clear()
	h.startMetadataSync(ctx, cat.ID)
	ev = withID(ev, cat.ID)
	ev.Changes = map[string]audit.Change{"name": {To: cat.Name}, "trino_catalog_name": {To: cat.TrinoName()}, "warehouse": {To: cat.WarehouseName()}}
	h.audited(ctx, ev.Allowed())
	return CreateCatalog201JSONResponse(h.catalogResponse(ctx, cat, binding, proj.Version)), nil
}

// upsertPrimaryBinding writes the managed binding a catalog's Trino columns
// describe, on its cluster's engine, entering state with the version it
// wants.
func upsertPrimaryBinding(ctx context.Context, q db.Querier, cluster db.TrinoCluster, c db.Catalog, state, version string) (db.CatalogBinding, error) {
	return q.UpsertManagedBinding(ctx, db.UpsertManagedBindingParams{
		CatalogID: c.ID, EngineID: cluster.EngineID, EngineCatalogName: c.TrinoName(),
		Connector: c.Connector, ExtraProps: c.ExtraProps, Enabled: c.Enabled,
		LifecycleState: state, DesiredVersion: &version,
	})
}

// projectBinding projects a binding of conn through the Trino binder.
func (h *Handlers) projectBinding(conn db.CatalogConnection, spec bindings.BindingSpec) (bindings.Projection, error) {
	_, cfg, err := h.connectionConfig(conn)
	if err != nil {
		return bindings.Projection{}, err
	}
	return h.binder.Project(spec, cfg)
}

// catalogSpec is the binding a catalog row describes. Stored extra_props
// must be a flat string map; anything else is an invalid binding.
func catalogSpec(cat db.Catalog) (bindings.BindingSpec, error) {
	extra := map[string]string{}
	if len(cat.ExtraProps) > 0 && string(cat.ExtraProps) != "null" {
		if err := json.Unmarshal(cat.ExtraProps, &extra); err != nil {
			return bindings.BindingSpec{}, fmt.Errorf("%w: extra_props must be a flat string map", bindings.ErrInvalid)
		}
	}
	return bindings.BindingSpec{EngineCatalog: cat.TrinoName(), Connector: cat.ConnectorName(), SourceRef: cat.WarehouseName(), ExtraProps: extra}, nil
}

// projectCatalog projects a stored catalog.
func (h *Handlers) projectCatalog(conn db.CatalogConnection, cat db.Catalog) (bindings.Projection, error) {
	spec, err := catalogSpec(cat)
	if err != nil {
		return bindings.Projection{}, err
	}
	return h.projectBinding(conn, spec)
}

// bindingInvalid reports a binding the request got wrong (422), as opposed
// to a server failure.
func bindingInvalid(err error) bool {
	return errors.Is(err, bindings.ErrInvalid) || errors.Is(err, bindings.ErrPropertyNotOverridable) ||
		errors.Is(err, bindings.ErrReservedName) || errors.Is(err, bindings.ErrUnsupportedSource) ||
		errors.Is(err, catalogs.ErrCredentialsUnreadable)
}

func bindingUnprocessable(err error) UnprocessableEntityJSONResponse {
	code := "validation_error"
	switch {
	case errors.Is(err, bindings.ErrPropertyNotOverridable):
		code = "property_not_overridable"
	case errors.Is(err, bindings.ErrReservedName):
		code = "reserved_name"
	case errors.Is(err, bindings.ErrUnsupportedSource):
		code = "binding_unsupported"
	case errors.Is(err, catalogs.ErrCredentialsUnreadable):
		code = "credentials_unreadable"
	}
	field, msg := "", err.Error()
	var pe *bindings.PropertyError
	if errors.As(err, &pe) {
		field, msg = "extra_props."+pe.Key, pe.Err.Error()
	}
	out := UnprocessableEntityJSONResponse{Code: code, Message: err.Error(), Errors: []struct {
		Field   *string `json:"field,omitempty"`
		Message *string `json:"message,omitempty"`
	}{{Message: &msg}}}
	if field != "" {
		out.Errors[0].Field = &field
	}
	return out
}

// catalogAccessError maps an authz error to the generic not-found/forbidden
// messages; ok is false for any other error.
func catalogAccessError(err error) (notFound, forbidden bool) {
	return errors.Is(err, authz.ErrNotFound), errors.Is(err, authz.ErrForbidden)
}

// GetCatalog — GET /connections/{connection_id}/catalogs/{catalog_id}
func (h *Handlers) GetCatalog(ctx context.Context, request GetCatalogRequestObject) (GetCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	cat, _, err := h.authz().CatalogOfConnection(ctx, p, connID, id, authz.ActRead)
	if err != nil {
		if nf, _ := catalogAccessError(err); nf {
			return GetCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
		}
		return nil, err
	}
	return GetCatalog200JSONResponse(h.catalogWithStatus(ctx, cat)), nil
}

// UpdateCatalog — PATCH /connections/{connection_id}/catalogs/{catalog_id}
// Rename (trino_catalog_name change) → DROP old + soft-delete the old registry
// row + UPDATE the domain row (including the new name) + re-create under the
// new name. Other changes: UPDATE + DROP + registry upsert + CREATE.
func (h *Handlers) UpdateCatalog(ctx context.Context, request UpdateCatalogRequestObject) (UpdateCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.CatalogUpdate, ResourceType: "catalog", ResourceID: idString(id)}
	ctx = audit.WithAction(ctx, ev.Action)
	existing, conn, err := h.authz().CatalogOfConnection(ctx, p, connID, id, authz.ActWrite)
	if err != nil {
		nf, fb := catalogAccessError(err)
		switch {
		case nf:
			return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
		case fb:
			return UpdateCatalog403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to modify this catalog"}}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "missing body"}}, nil
	}
	if existing.Origin != "source" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateCatalog422JSONResponse{UnprocessableEntityJSONResponse: notManaged()}, nil
	}
	body := *request.Body

	newTrinoName := existing.TrinoName()
	if body.TrinoCatalogName != "" {
		newTrinoName = body.TrinoCatalogName
	}
	newWarehouse := existing.WarehouseName()
	if body.Warehouse != "" {
		newWarehouse = body.Warehouse
	}
	newConnector := existing.ConnectorName()
	if body.Connector != nil && *body.Connector != "" {
		newConnector = *body.Connector
	}
	newName := existing.Name
	if body.Name != "" {
		newName = body.Name
	}
	extraProps := existing.ExtraProps
	if body.ExtraProps != nil && len(*body.ExtraProps) > 0 {
		extraProps, _ = json.Marshal(*body.ExtraProps)
	}
	if newTrinoName != existing.TrinoName() {
		ev.Action = audit.CatalogRename
	}
	ev.Changes = catalogChanges(existing, newName, newTrinoName, newWarehouse, newConnector, extraProps)

	// The new binding must project before anything changes.
	next := existing
	next.TrinoCatalogName, next.Warehouse, next.Connector, next.ExtraProps = &newTrinoName, &newWarehouse, &newConnector, extraProps
	proj, err := h.projectCatalog(conn, next)
	if bindingInvalid(err) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateCatalog422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}

	cluster, eng, err := h.catalogEngine(ctx, existing)
	if err != nil {
		return nil, err
	}
	cur, err := h.queries.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: existing.ID, EngineID: eng.ID})
	if err != nil {
		return nil, fmt.Errorf("catalog binding: %w", err)
	}
	var updated db.Catalog
	var binding db.CatalogBinding
	if err := h.inTx(ctx, func(q db.Querier) error {
		u, err := q.UpdateCatalog(ctx, db.UpdateCatalogParams{
			ID:               id,
			Name:             newName,
			Description:      body.Description,
			Warehouse:        &newWarehouse,
			Connector:        &proj.Connector,
			ExtraProps:       extraProps,
			TrinoCatalogName: &newTrinoName,
		})
		if err != nil {
			return err
		}
		updated = u
		// Guarded: only an active or failed binding may start a change; a
		// deleted catalog (disabled binding) is never re-created here.
		binding, err = q.UpdateBinding(ctx, db.UpdateBindingParams{
			ID: cur.ID, EngineCatalogName: u.TrinoName(), Connector: u.Connector, ExtraProps: u.ExtraProps, Enabled: cur.Enabled,
			LifecycleState: bindings.StateUpdating, DesiredVersion: &proj.Version, FromStates: []string{bindings.StateActive, bindings.StateError},
		})
		return err
	}); errors.Is(err, pgx.ErrNoRows) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalog409JSONResponse{ConflictJSONResponse: concurrentChange(cur)}, nil
	} else if err != nil {
		return nil, fmt.Errorf("update catalog: %w", err)
	}

	l := h.lifecycleOn(cluster, eng)
	if newTrinoName != existing.TrinoName() {
		binding, err = l.Rename(ctx, binding, existing.TrinoName(), cur.DesiredVersion, proj)
		if err == nil && binding.EngineCatalogName == existing.TrinoName() {
			// The new name could not be created: the catalog keeps its old one.
			old := existing.TrinoName()
			updated, err = h.queries.UpdateCatalog(ctx, db.UpdateCatalogParams{
				ID: id, Name: updated.Name, Description: updated.Description, Warehouse: updated.Warehouse,
				Connector: updated.Connector, ExtraProps: updated.ExtraProps, TrinoCatalogName: &old,
			})
		}
	} else {
		binding, err = l.Update(ctx, binding, proj)
	}
	if errors.Is(err, bindings.ErrConcurrentChange) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalog409JSONResponse{ConflictJSONResponse: concurrentChange(cur)}, nil
	}
	if err != nil {
		return nil, err
	}
	observe(ctx, l)
	h.catalogCache.Clear()
	h.startMetadataSync(ctx, updated.ID)
	h.audited(ctx, ev.Allowed())
	return UpdateCatalog200JSONResponse(h.catalogResponse(ctx, updated, binding, proj.Version)), nil
}

// DeleteCatalog — DELETE /connections/{connection_id}/catalogs/{catalog_id}
// Soft delete (enabled=false) + soft-delete registry row + DROP at Trino.
func (h *Handlers) DeleteCatalog(ctx context.Context, request DeleteCatalogRequestObject) (DeleteCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.CatalogDelete, ResourceType: "catalog", ResourceID: idString(id)}
	ctx = audit.WithAction(ctx, ev.Action)
	cat, _, err := h.authz().CatalogOfConnection(ctx, p, connID, id, authz.ActWrite)
	if err != nil {
		nf, fb := catalogAccessError(err)
		switch {
		case nf:
			return DeleteCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
		case fb:
			return DeleteCatalog403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to delete this catalog"}}, nil
		}
		return nil, err
	}

	if cat.Origin != "source" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteCatalog422JSONResponse{UnprocessableEntityJSONResponse: notManaged()}, nil
	}
	cluster, eng, err := h.catalogEngine(ctx, cat)
	if err != nil {
		return nil, err
	}
	binding, err := h.queries.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: cat.ID, EngineID: eng.ID})
	if err != nil {
		return nil, fmt.Errorf("catalog binding: %w", err)
	}
	if err := h.inTx(ctx, func(q db.Querier) error {
		if err := q.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: id, Enabled: false}); err != nil {
			return err
		}
		return q.DisableManagedBindingsOfCatalog(ctx, id)
	}); err != nil {
		return nil, err
	}
	// A failed drop leaves the binding deprovisioning with the error; the
	// reconciler (#37) retries. The catalog is gone for users either way.
	l := h.lifecycleOn(cluster, eng)
	if _, err := l.Deprovision(ctx, binding); err != nil && !errors.Is(err, bindings.ErrConcurrentChange) {
		return nil, err
	}
	observe(ctx, l)
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalog204Response{}, nil
}

// SyncCatalog — POST /catalogs/{catalog_id}/sync
func (h *Handlers) SyncCatalog(ctx context.Context, request SyncCatalogRequestObject) (SyncCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SyncCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.CatalogSync, ResourceType: "catalog", ResourceID: idString(id)}
	ctx = audit.WithAction(ctx, ev.Action)
	cat, conn, err := h.authz().Catalog(ctx, p, id, authz.ActWrite)
	if err != nil {
		nf, fb := catalogAccessError(err)
		switch {
		case nf:
			return SyncCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
		case fb:
			return SyncCatalog403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "not allowed to sync this catalog"}}, nil
		}
		return nil, err
	}
	if cat.Origin != "source" {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return SyncCatalog422JSONResponse{UnprocessableEntityJSONResponse: notManaged()}, nil
	}
	cluster, eng, err := h.catalogEngine(ctx, cat)
	if err != nil {
		return nil, err
	}
	binding, err := h.queries.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: cat.ID, EngineID: eng.ID})
	if err != nil {
		return nil, fmt.Errorf("catalog binding: %w", err)
	}
	// A projection failure (e.g. unreadable credentials) fails closed before
	// touching Trino.
	proj, err := h.projectCatalog(conn, cat)
	if err != nil {
		c := h.catalogResponse(ctx, cat, binding, "")
		msg, status := err.Error(), CatalogSyncStatusError
		c.SyncError, c.SyncStatus = &msg, &status
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalog409JSONResponse(c), nil
	}
	l := h.lifecycleOn(cluster, eng)
	synced, err := l.Sync(ctx, binding, proj)
	if errors.Is(err, bindings.ErrConcurrentChange) {
		c := h.catalogResponse(ctx, cat, binding, "")
		msg := "the catalog's binding is " + binding.LifecycleState + "; it cannot be synced now"
		c.SyncError = &msg
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalog409JSONResponse(c), nil
	}
	if err != nil {
		return nil, err
	}
	binding = synced
	observe(ctx, l)
	c := h.catalogResponse(ctx, cat, binding, proj.Version)
	if binding.LifecycleState != bindings.StateActive {
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalog409JSONResponse(c), nil
	}
	h.audited(ctx, ev.Allowed())
	return SyncCatalog200JSONResponse(c), nil
}

// ReconcileTrino — POST /trino/reconcile
// Read-only comparison of the registry vs live Trino catalog list. It exposes
// every catalog in the cluster, so it is for global administrators only.
func (h *Handlers) ReconcileTrino(ctx context.Context, _ ReconcileTrinoRequestObject) (ReconcileTrinoResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ReconcileTrino401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return ReconcileTrino403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "administrator role required"}}, nil
	}
	registry, err := h.queries.ListEnabledCatalogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled catalogs: %w", err)
	}
	live, err := h.binder.ListCatalogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list trino catalogs: %w", err)
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, n := range live {
		liveSet[n] = struct{}{}
	}

	pending := make([]Catalog, 0)
	ok := make([]Catalog, 0)
	for _, c := range registry {
		pub := dbCatalogToPublic(c)
		if _, present := liveSet[c.TrinoName()]; present {
			ok = append(ok, pub)
		} else {
			pending = append(pending, pub)
		}
		delete(liveSet, c.TrinoName())
	}
	orphan := make([]string, 0, len(liveSet))
	for n := range liveSet {
		orphan = append(orphan, n)
	}
	h.audited(ctx, audit.Event{Action: audit.CatalogReconcile, ResourceType: "engine", Outcome: audit.Allowed})
	return ReconcileTrino200JSONResponse{
		Pending:       &pending,
		OrphanInTrino: &orphan,
		Ok:            &ok,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────

func dbCatalogToPublic(c db.Catalog) Catalog {
	id := openapi_types.UUID(c.ID.Bytes)
	connID := openapi_types.UUID(c.ConnectionID.Bytes)
	out := Catalog{
		Id:               &id,
		ConnectionId:     &connID,
		Name:             &c.Name,
		TrinoCatalogName: c.TrinoCatalogName,
		Warehouse:        c.Warehouse,
		Connector:        c.Connector,
		Enabled:          &c.Enabled,
		CreatedAt:        pgTimestampToTimePtr(c.CreatedAt),
		UpdatedAt:        pgTimestampToTimePtr(c.UpdatedAt),
		Description:      c.Description,
	}
	return out
}

func pgTimestampToTimePtr(ts pgtype.Timestamptz) *time.Time {
	t := pgTimestampToTime(ts)
	if t.IsZero() {
		return nil
	}
	return &t
}

// (time.Time alias kept local; pgTimestampToTime lives in handlers.go)
// ─────────────────────────────────────────────────────────────────────────
// tx helper
// ─────────────────────────────────────────────────────────────────────────

func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ptrSyncStatus(s CatalogSyncStatus) *CatalogSyncStatus { return &s }
