package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/db"
)

// Glue between the handlers and the binding lifecycle (#38): the catalog
// endpoints act on a catalog's primary binding (its managed binding on its
// Trino cluster's engine), the binding endpoints on any managed binding.

// lifecycleOn is the lifecycle of the bindings of one Trino engine.
func (h *Handlers) lifecycleOn(cluster db.TrinoCluster, eng db.Engine) bindings.Lifecycle {
	return bindings.Lifecycle{
		Store:        h.queries,
		Binder:       h.binder.WithStore(h.queries),
		Engine:       bindings.EngineRef{ID: eng.ID, ClusterID: cluster.ID},
		ConnectionID: eng.ConnectionID,
		DDLTimeout:   catalogDDLTimeout,
		OnDDL:        h.metrics.catalog.DDL,
	}
}

// catalogEngine is the Trino cluster and engine a catalog's Trino columns
// name.
func (h *Handlers) catalogEngine(ctx context.Context, cat db.Catalog) (db.TrinoCluster, db.Engine, error) {
	cluster, err := h.queries.GetTrinoClusterByID(ctx, cat.ClusterID)
	if err != nil {
		return db.TrinoCluster{}, db.Engine{}, err
	}
	if !cluster.EngineID.Valid {
		return db.TrinoCluster{}, db.Engine{}, errors.New("the catalog's Trino cluster has no engine")
	}
	eng, err := h.queries.GetEngineByID(ctx, cluster.EngineID)
	return cluster, eng, err
}

// observeTimeout bounds the observation that follows a procedure.
const observeTimeout = 10 * time.Second

// concurrentChange is the 409 for a binding that left the state a request
// expected.
func concurrentChange(b db.CatalogBinding) ConflictJSONResponse {
	msg := "the binding changed while this request ran; retry"
	if b.LifecycleState != "" {
		msg = "the binding is " + b.LifecycleState + "; retry when it settles"
	}
	return ConflictJSONResponse{Code: "concurrent_change", Message: msg}
}

// notManaged is the 422 for catalogs Baleia does not manage (native and
// adopted catalogs belong to their engine).
func notManaged() UnprocessableEntityJSONResponse {
	return unprocessable("catalog_not_managed", "this catalog belongs to its engine; Baleia does not change it")
}

// observe records what the engine reports after a procedure; it never fails
// the request (the reconciler, #37, observes again).
func observe(ctx context.Context, l bindings.Lifecycle) {
	// Bounded: it outlives a disconnected client, never a stuck engine.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observeTimeout)
	defer cancel()
	if _, err := l.Observe(ctx); err != nil {
		slog.Warn("observe engine catalogs", "engine", l.Engine.ID, "err", err)
	}
}

// bindingSyncStatus is the derived status of a binding (binding_status_v).
func (h *Handlers) bindingSyncStatus(ctx context.Context, b db.CatalogBinding) string {
	st, err := h.queries.GetBindingStatus(ctx, b.ID)
	if err != nil || st.SyncStatus == "" {
		return "pending"
	}
	return st.SyncStatus
}

// catalogResponse is a catalog with its primary binding's status.
func (h *Handlers) catalogResponse(ctx context.Context, cat db.Catalog, b db.CatalogBinding, version string) Catalog {
	c := dbCatalogToPublic(cat)
	status := CatalogSyncStatus(h.bindingSyncStatus(ctx, b))
	c.SyncStatus = &status
	c.SyncError = b.LastErrorMessage
	if version != "" && status == CatalogSyncStatusSynced {
		c.CatalogVersion = &version
	}
	return c
}

// bindingResponse is a binding with its derived status and lifecycle.
func (h *Handlers) bindingResponse(ctx context.Context, b db.CatalogBinding, version string) CatalogBinding {
	out := bindingToPublic(b)
	status := CatalogBindingSyncStatus(h.bindingSyncStatus(ctx, b))
	out.SyncStatus = &status
	if version != "" && status == CatalogBindingSyncStatusSynced {
		out.Version = &version
	}
	return out
}

// catalogWithStatus is a stored catalog with its primary binding's status,
// or the bare catalog when it has none (native catalogs).
func (h *Handlers) catalogWithStatus(ctx context.Context, cat db.Catalog) Catalog {
	if cat.Origin != "source" || !cat.ClusterID.Valid {
		return dbCatalogToPublic(cat)
	}
	_, eng, err := h.catalogEngine(ctx, cat)
	if err != nil {
		return dbCatalogToPublic(cat)
	}
	b, err := h.queries.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: cat.ID, EngineID: eng.ID})
	if err != nil {
		return dbCatalogToPublic(cat)
	}
	c := h.catalogResponse(ctx, cat, b, "")
	c.CatalogVersion = b.DesiredVersion
	return c
}
