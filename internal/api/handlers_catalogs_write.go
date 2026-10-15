package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/catalogs"
	"log/slog"
	"time"

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

func (h *Handlers) authz() *authz.Checker { return authz.New(h.queries) }

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
			out = append(out, dbCatalogToPublic(r))
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
		out = append(out, dbCatalogToPublic(r))
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
		return CreateCatalog422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: "validation_error", Message: "missing body"}}, nil
	}
	body := *request.Body
	if body.TrinoCatalogName == "" || body.Name == "" || body.Warehouse == "" {
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
		return CreateCatalog422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	extraProps, _ := json.Marshal(extra)
	eng := bindings.EngineRef{ClusterID: cluster.ID}

	var cat db.Catalog
	txErr := withTx(ctx, h.pool, func(tx pgx.Tx) error {
		qtx := db.New(tx)
		c, err := qtx.CreateCatalog(ctx, db.CreateCatalogParams{
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
		if err := upsertPrimaryBinding(ctx, qtx, cluster, c); err != nil {
			return err
		}
		return h.binder.WithStore(qtx).Stage(ctx, eng, proj)
	})
	if txErr != nil {
		return nil, fmt.Errorf("create catalog: %w", txErr)
	}

	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.binder.Apply(ddlCtx, proj); err != nil {
		c := h.markSyncError(ctx, cat, err)
		return CreateCatalog201JSONResponse(c), nil
	}

	h.catalogCache.Clear()
	c := dbCatalogToPublic(cat)
	c.CatalogVersion = &proj.Version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return CreateCatalog201JSONResponse(c), nil
}

// upsertPrimaryBinding writes the managed binding a catalog's Trino columns
// describe, on its cluster's engine (the 00024 trigger did this until #80).
func upsertPrimaryBinding(ctx context.Context, q db.Querier, cluster db.TrinoCluster, c db.Catalog) error {
	if !cluster.EngineID.Valid || c.Origin != "source" {
		return nil
	}
	_, err := q.UpsertManagedBinding(ctx, db.UpsertManagedBindingParams{
		CatalogID: c.ID, EngineID: cluster.EngineID, EngineCatalogName: c.TrinoName(),
		Connector: c.Connector, ExtraProps: c.ExtraProps, Enabled: c.Enabled,
	})
	return err
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

// markSyncError records err on the catalog's registry row and returns the
// catalog as the API shows it.
func (h *Handlers) markSyncError(ctx context.Context, cat db.Catalog, err error) Catalog {
	errMsg := err.Error()
	_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{
		ClusterID:   cat.ClusterID,
		CatalogName: cat.TrinoName(),
		SyncError:   &errMsg,
	})
	c := dbCatalogToPublic(cat)
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusError)
	c.SyncError = &errMsg
	return c
}

// recreateCatalog replaces the live Trino catalog with the catalog's current
// projection (binder Replace) and returns the new catalog version.
func (h *Handlers) recreateCatalog(ctx context.Context, cat db.Catalog, conn db.CatalogConnection) (string, error) {
	proj, err := h.projectCatalog(conn, cat)
	if err != nil {
		return "", err
	}
	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.binder.WithStore(h.queries).Replace(ddlCtx, bindings.EngineRef{ClusterID: cat.ClusterID}, proj); err != nil {
		return "", err
	}
	return proj.Version, nil
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
	return GetCatalog200JSONResponse(dbCatalogToPublic(cat)), nil
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

	// The new binding must project before anything changes.
	next := existing
	next.TrinoCatalogName, next.Warehouse, next.Connector, next.ExtraProps = &newTrinoName, &newWarehouse, &newConnector, extraProps
	proj, err := h.projectCatalog(conn, next)
	if bindingInvalid(err) {
		return UpdateCatalog422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}

	var updated db.Catalog
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
		cluster, err := q.GetTrinoClusterByID(ctx, u.ClusterID)
		if err != nil {
			return err
		}
		return upsertPrimaryBinding(ctx, q, cluster, u)
	}); err != nil {
		return nil, fmt.Errorf("update catalog: %w", err)
	}

	eng := bindings.EngineRef{ClusterID: updated.ClusterID}
	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	b := h.binder.WithStore(h.queries)
	if newTrinoName != existing.TrinoName() {
		err = b.Rename(ddlCtx, eng, existing.TrinoName(), proj) // create the new name, then drop the old
	} else {
		err = b.Replace(ddlCtx, eng, proj)
	}
	h.catalogCache.Clear()
	if err != nil {
		slog.Warn("apply catalog after update", "err", err)
		return UpdateCatalog200JSONResponse(h.markSyncError(ctx, updated, err)), nil
	}
	c := dbCatalogToPublic(updated)
	c.CatalogVersion = &proj.Version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return UpdateCatalog200JSONResponse(c), nil
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

	txErr := withTx(ctx, h.pool, func(tx pgx.Tx) error {
		qtx := db.New(tx)
		if err := qtx.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: id, Enabled: false}); err != nil {
			return err
		}
		if err := qtx.DisableManagedBindingsOfCatalog(ctx, id); err != nil {
			return err
		}
		return h.binder.WithStore(qtx).Unstage(ctx, bindings.EngineRef{ClusterID: cat.ClusterID}, cat.TrinoName())
	})
	if txErr != nil {
		return nil, txErr
	}
	dropCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.binder.Remove(dropCtx, cat.TrinoName()); err != nil {
		slog.Warn("drop catalog", "catalog", cat.TrinoName(), "err", err)
	}
	h.catalogCache.Clear()
	return DeleteCatalog204Response{}, nil
}

// SyncCatalog — POST /catalogs/{catalog_id}/sync
func (h *Handlers) SyncCatalog(ctx context.Context, request SyncCatalogRequestObject) (SyncCatalogResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SyncCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
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
	// A projection failure (e.g. unreadable credentials) fails closed before
	// touching Trino: the live catalog is left as is and the registry row
	// records why it could not be re-synced.
	version, err := h.recreateCatalog(ctx, cat, conn)
	if err != nil {
		return h.syncFailed(ctx, cat, err), nil
	}

	c := dbCatalogToPublic(cat)
	c.CatalogVersion = &version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return SyncCatalog200JSONResponse(c), nil
}

// syncFailed records err on the registry row and builds the 409 response.
func (h *Handlers) syncFailed(ctx context.Context, cat db.Catalog, err error) SyncCatalogResponseObject {
	return SyncCatalog409JSONResponse(h.markSyncError(ctx, cat, err))
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
