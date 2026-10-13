package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	catalogs "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/db"
	trino_client "github.com/galileostudio/baleia/internal/trino"
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
		if errors.Is(err, authz.ErrNotFound) {
			return ListCatalogs404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
		}
		return nil, err
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
// Write-through flow. See CR-017 §I7b.
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
		return CreateCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "missing body"}}, nil
	}
	body := *request.Body
	if body.TrinoCatalogName == "" || body.Name == "" || body.Warehouse == "" {
		return CreateCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "trino_catalog_name, name, warehouse are required"}}, nil
	}

	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("get default cluster: %w", err)
	}
	creds, err := catalogs.DecodeCredentials(conn, h.keyring)
	if err != nil {
		slog.Error("decode creds for create", "err", err)
		return nil, err
	}

	connector := "iceberg"
	if body.Connector != nil && *body.Connector != "" {
		connector = *body.Connector
	}
	extraProps := []byte("{}")
	if body.ExtraProps != nil {
		extraProps, _ = json.Marshal(*body.ExtraProps)
	}

	cat, version, txErr := h.createCatalogTx(ctx, db.CreateCatalogParams{
		ConnectionID:     connID,
		Name:             body.Name,
		Description:      body.Description,
		TrinoCatalogName: body.TrinoCatalogName,
		ClusterID:        cluster.ID,
		Warehouse:        body.Warehouse,
		Connector:        connector,
		ExtraProps:       extraProps,
	}, conn, creds)
	if txErr != nil {
		return nil, fmt.Errorf("create catalog: %w", txErr)
	}

	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.emitCreateCatalog(ddlCtx, cat, conn, creds); err != nil {
		errPtr := err.Error()
		_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{
			ClusterID:   cluster.ID,
			CatalogName: cat.TrinoCatalogName,
			SyncError:   &errPtr,
		})
		c := dbCatalogToPublic(cat)
		c.SyncStatus = ptrSyncStatus(CatalogSyncStatusError)
		c.SyncError = &errPtr
		return CreateCatalog201JSONResponse(c), nil
	}

	h.catalogCache.Clear()
	c := dbCatalogToPublic(cat)
	c.CatalogVersion = &version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return CreateCatalog201JSONResponse(c), nil
}

// createCatalogTx wraps the domain-row INSERT + registry UPSERT in a single
// transaction. If either fails, everything rolls back.
func (h *Handlers) createCatalogTx(ctx context.Context, params db.CreateCatalogParams, conn db.CatalogConnection, creds catalogs.Credentials) (db.Catalog, string, error) {
	var created db.Catalog
	var version string

	txErr := withTx(ctx, h.pool, func(tx pgx.Tx) error {
		qtx := db.New(tx)
		c, err := qtx.CreateCatalog(ctx, params)
		if err != nil {
			return err
		}
		created = c

		proj, err := catalogs.Project(conn, c, creds)
		if err != nil {
			return err
		}
		version = catalogs.Version(proj.Row.CatalogName, proj.Row.ConnectorName, proj.Row.Properties)

		propsJSON, _ := json.Marshal(proj.Row.Properties)
		return qtx.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
			ClusterID:      pgtype.UUID{Bytes: proj.Row.ClusterID, Valid: true},
			CatalogName:    proj.Row.CatalogName,
			ConnectorName:  proj.Row.ConnectorName,
			Column4:        propsJSON,
			CatalogVersion: &version,
		})
	})
	if txErr != nil {
		return db.Catalog{}, "", txErr
	}
	return created, version, nil
}

// emitCreateCatalog sends CREATE CATALOG to Trino with the spec built from
// the Projection. SecretKeys ensure the DDL text carries only @baleia-secret
// placeholders — never the real secret. The caller supplies the deadline.
func (h *Handlers) emitCreateCatalog(ctx context.Context, cat db.Catalog, conn db.CatalogConnection, creds catalogs.Credentials) error {
	proj, err := catalogs.Project(conn, cat, creds)
	if err != nil {
		return err
	}
	return h.trino.CreateCatalog(ctx, trino_client.CatalogSpec{
		Name:       proj.Row.CatalogName,
		Connector:  proj.Row.ConnectorName,
		Properties: proj.Row.Properties,
		SecretKeys: proj.SecretKeys,
	})
}

// recreateCatalog replaces the live Trino catalog with the current projection
// and returns the new catalog version. The order is load-bearing: the plugin
// soft-disables the registry row on DROP CATALOG, and CREATE CATALOG resolves
// its @baleia-secret placeholders from an enabled row, so the row is upserted
// (re-enabled, with the new properties) between the two.
func (h *Handlers) recreateCatalog(ctx context.Context, cat db.Catalog, conn db.CatalogConnection, creds catalogs.Credentials, dropName string) (string, error) {
	proj, err := catalogs.Project(conn, cat, creds)
	if err != nil {
		return "", fmt.Errorf("project: %w", err)
	}
	version := catalogs.Version(proj.Row.CatalogName, proj.Row.ConnectorName, proj.Row.Properties)
	propsJSON, err := json.Marshal(proj.Row.Properties)
	if err != nil {
		return "", fmt.Errorf("marshal properties: %w", err)
	}

	ddlCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	if err := h.trino.DropCatalog(ddlCtx, dropName); err != nil {
		slog.Warn("drop catalog before re-create", "catalog", dropName, "err", err)
	}
	if err := h.queries.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID:      pgtype.UUID{Bytes: proj.Row.ClusterID, Valid: true},
		CatalogName:    proj.Row.CatalogName,
		ConnectorName:  proj.Row.ConnectorName,
		Column4:        propsJSON,
		CatalogVersion: &version,
	}); err != nil {
		return "", fmt.Errorf("upsert registry: %w", err)
	}
	if err := h.emitCreateCatalog(ddlCtx, cat, conn, creds); err != nil {
		return "", err
	}
	return version, nil
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
	creds, err := catalogs.DecodeCredentials(conn, h.keyring)
	if err != nil {
		slog.Error("decode creds for update", "err", err)
		return nil, err
	}

	newTrinoName := existing.TrinoCatalogName
	if body.TrinoCatalogName != "" {
		newTrinoName = body.TrinoCatalogName
	}
	newWarehouse := existing.Warehouse
	if body.Warehouse != "" {
		newWarehouse = body.Warehouse
	}
	newConnector := existing.Connector
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

	// Rename: DROP the old name and soft-delete its registry row first.
	if newTrinoName != existing.TrinoCatalogName {
		dropCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
		_ = h.trino.DropCatalog(dropCtx, existing.TrinoCatalogName)
		cancel()
		_ = h.queries.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{
			ClusterID:   existing.ClusterID,
			CatalogName: existing.TrinoCatalogName,
		})
	}

	updated, err := h.queries.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID:               id,
		Name:             newName,
		Description:      body.Description,
		Warehouse:        newWarehouse,
		Connector:        newConnector,
		ExtraProps:       extraProps,
		TrinoCatalogName: newTrinoName,
	})
	if err != nil {
		return nil, fmt.Errorf("update catalog: %w", err)
	}

	version, err := h.recreateCatalog(ctx, updated, conn, creds, updated.TrinoCatalogName)
	c := dbCatalogToPublic(updated)
	if err != nil {
		slog.Warn("re-create catalog after update", "err", err)
		errMsg := err.Error()
		_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{
			ClusterID:   updated.ClusterID,
			CatalogName: updated.TrinoCatalogName,
			SyncError:   &errMsg,
		})
		c.SyncStatus = ptrSyncStatus(CatalogSyncStatusError)
		c.SyncError = &errMsg
		h.catalogCache.Clear()
		return UpdateCatalog200JSONResponse(c), nil
	}

	h.catalogCache.Clear()
	c.CatalogVersion = &version
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
		return qtx.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{
			ClusterID:   cat.ClusterID,
			CatalogName: cat.TrinoCatalogName,
		})
	})
	if txErr != nil {
		return nil, txErr
	}
	dropCtx, cancel := context.WithTimeout(ctx, catalogDDLTimeout)
	defer cancel()
	_ = h.trino.DropCatalog(dropCtx, cat.TrinoCatalogName)
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
	creds, err := catalogs.DecodeCredentials(conn, h.keyring)
	if err != nil {
		// Fail closed before touching Trino: the live catalog is left as is and
		// the registry row records why it could not be re-synced.
		slog.Error("decode creds for sync", "err", err)
		return h.syncFailed(ctx, cat, err), nil
	}

	version, err := h.recreateCatalog(ctx, cat, conn, creds, cat.TrinoCatalogName)
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
	errMsg := err.Error()
	_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{
		ClusterID:   cat.ClusterID,
		CatalogName: cat.TrinoCatalogName,
		SyncError:   &errMsg,
	})
	c := dbCatalogToPublic(cat)
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusError)
	c.SyncError = &errMsg
	return SyncCatalog409JSONResponse(c)
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
	live, err := h.trino.ListCatalogs(ctx)
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
		if _, present := liveSet[c.TrinoCatalogName]; present {
			ok = append(ok, pub)
		} else {
			pending = append(pending, pub)
		}
		delete(liveSet, c.TrinoCatalogName)
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
		TrinoCatalogName: &c.TrinoCatalogName,
		Warehouse:        &c.Warehouse,
		Connector:        &c.Connector,
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
