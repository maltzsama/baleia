package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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

// ListCatalogs — GET /connections/{connection_id}/catalogs
func (h *Handlers) ListCatalogs(ctx context.Context, request ListCatalogsRequestObject) (ListCatalogsResponseObject, error) {
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
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
	connID := pgtype.UUID{Bytes: request.ConnectionId, Valid: true}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		return CreateCatalog401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "connection not found"}}, nil
	}
	creds, err := catalogs.DecodeCredentials(conn, h.cfg.JWTSecret)
	if err != nil {
		slog.Error("decode creds for create", "err", err)
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
		ConnectionID:      connID,
		Name:              body.Name,
		Description:       body.Description,
		TrinoCatalogName: body.TrinoCatalogName,
		ClusterID:         cluster.ID,
		Warehouse:        body.Warehouse,
		Connector:        connector,
		ExtraProps:       extraProps,
	}, conn, creds)
	if txErr != nil {
		return nil, fmt.Errorf("create catalog: %w", txErr)
	}

	if err := h.emitCreateCatalog(ctx, cat, conn, creds); err != nil {
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
// placeholders — never the real secret.
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

// GetCatalog — GET /connections/{connection_id}/catalogs/{catalog_id}
func (h *Handlers) GetCatalog(ctx context.Context, request GetCatalogRequestObject) (GetCatalogResponseObject, error) {
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	cat, err := h.queries.GetCatalogByID(ctx, id)
	if err != nil {
		return GetCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}
	return GetCatalog200JSONResponse(dbCatalogToPublic(cat)), nil
}

// UpdateCatalog — PATCH /connections/{connection_id}/catalogs/{catalog_id}
// Rename (trino_catalog_name change) → DROP old + soft-delete registry +
// UPDATE domain row + INSERT new registry row + CREATE new. Other changes:
// UPDATE + DROP+CREATE.
func (h *Handlers) UpdateCatalog(ctx context.Context, request UpdateCatalogRequestObject) (UpdateCatalogResponseObject, error) {
	if request.Body == nil {
		return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "missing body"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	existing, err := h.queries.GetCatalogByID(ctx, id)
	if err != nil {
		return UpdateCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}

	body := *request.Body
	conn, err := h.queries.GetCatalogConnectionByID(ctx, existing.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	creds, _ := catalogs.DecodeCredentials(conn, h.cfg.JWTSecret)

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

	// Rename: DROP old name then soft-delete the old registry row.
	if newTrinoName != existing.TrinoCatalogName {
		_ = h.trino.DropCatalog(ctx, existing.TrinoCatalogName)
		_ = h.queries.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{
			ClusterID:   existing.ClusterID,
			CatalogName: existing.TrinoCatalogName,
		})
	}

	updated, err := h.queries.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID:          id,
		Name:        newName,
		Description: body.Description,
		Warehouse:   newWarehouse,
		Connector:   newConnector,
		ExtraProps:  extraProps,
	})
	if err != nil {
		return nil, fmt.Errorf("update catalog: %w", err)
	}
	if newTrinoName != existing.TrinoCatalogName {
		// UpdateCatalog query does not set trino_catalog_name; the rename
		// path needs a separate migration once sqlc exposes it. For now,
		// downstream projection uses existing name; TODO — see CR-017 §I7c.
		updated.TrinoCatalogName = newTrinoName
	}

	proj, err := catalogs.Project(conn, updated, creds)
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	version := catalogs.Version(proj.Row.CatalogName, proj.Row.ConnectorName, proj.Row.Properties)
	propsJSON, _ := json.Marshal(proj.Row.Properties)

	_ = h.queries.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID:      pgtype.UUID{Bytes: proj.Row.ClusterID, Valid: true},
		CatalogName:    proj.Row.CatalogName,
		ConnectorName:  proj.Row.ConnectorName,
		Column4:        propsJSON,
		CatalogVersion: &version,
	})

	// Re-emit DROP+CREATE at Trino (properties changed or renamed).
	_ = h.trino.DropCatalog(ctx, existing.TrinoCatalogName)
	if err := h.emitCreateCatalog(ctx, updated, conn, creds); err != nil {
		slog.Warn("re-create catalog after update", "err", err)
	}

	c := dbCatalogToPublic(updated)
	c.CatalogVersion = &version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return UpdateCatalog200JSONResponse(c), nil
}

// DeleteCatalog — DELETE /connections/{connection_id}/catalogs/{catalog_id}
// Soft delete (enabled=false) + soft-delete registry row + DROP at Trino.
func (h *Handlers) DeleteCatalog(ctx context.Context, request DeleteCatalogRequestObject) (DeleteCatalogResponseObject, error) {
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	cat, err := h.queries.GetCatalogByID(ctx, id)
	if err != nil {
		return DeleteCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
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
	_ = h.trino.DropCatalog(ctx, cat.TrinoCatalogName)
	h.catalogCache.Clear()
	return DeleteCatalog204Response{}, nil
}

// SyncCatalog — POST /catalogs/{catalog_id}/sync
func (h *Handlers) SyncCatalog(ctx context.Context, request SyncCatalogRequestObject) (SyncCatalogResponseObject, error) {
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	cat, err := h.queries.GetCatalogByID(ctx, id)
	if err != nil {
		return SyncCatalog404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog not found"}}, nil
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	creds, _ := catalogs.DecodeCredentials(conn, h.cfg.JWTSecret)

	_ = h.trino.DropCatalog(ctx, cat.TrinoCatalogName)
	if err := h.emitCreateCatalog(ctx, cat, conn, creds); err != nil {
		errMsg := err.Error()
		_ = h.queries.MarkTrinoCatalogRegistryError(ctx, db.MarkTrinoCatalogRegistryErrorParams{
			ClusterID:   cat.ClusterID,
			CatalogName: cat.TrinoCatalogName,
			SyncError:   &errMsg,
		})
		c := dbCatalogToPublic(cat)
		c.SyncStatus = ptrSyncStatus(CatalogSyncStatusError)
		c.SyncError = &errMsg
		return SyncCatalog409JSONResponse(c), nil
	}

	proj, _ := catalogs.Project(conn, cat, creds)
	version := catalogs.Version(proj.Row.CatalogName, proj.Row.ConnectorName, proj.Row.Properties)
	propsJSON, _ := json.Marshal(proj.Row.Properties)
	_ = h.queries.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID:      pgtype.UUID{Bytes: proj.Row.ClusterID, Valid: true},
		CatalogName:    proj.Row.CatalogName,
		ConnectorName:  proj.Row.ConnectorName,
		Column4:        propsJSON,
		CatalogVersion: &version,
	})

	c := dbCatalogToPublic(cat)
	c.CatalogVersion = &version
	c.SyncStatus = ptrSyncStatus(CatalogSyncStatusSynced)
	return SyncCatalog200JSONResponse(c), nil
}

// ReconcileTrino — POST /trino/reconcile
// Read-only comparison of the registry vs live Trino catalog list.
func (h *Handlers) ReconcileTrino(ctx context.Context, _ ReconcileTrinoRequestObject) (ReconcileTrinoResponseObject, error) {
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