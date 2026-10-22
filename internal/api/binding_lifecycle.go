package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Glue between the handlers and the binding lifecycle (#38): the catalog
// endpoints act on a catalog's primary binding (its managed binding on its
// Trino cluster's engine), the binding endpoints on any managed binding.

// lifecycleOn is the lifecycle of the bindings of one Trino engine.
func (h *CatalogHandlers) lifecycleOn(cluster db.TrinoCluster, eng db.Engine) bindings.Lifecycle {
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
func (h *core) catalogEngine(ctx context.Context, cat db.Catalog) (db.TrinoCluster, db.Engine, error) {
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
	return ConflictJSONResponse{Code: ErrorCodeConcurrentChange, Message: msg}
}

// notManaged is the 422 for catalogs Baleia does not manage (native and
// adopted catalogs belong to their engine).
func notManaged() UnprocessableEntityJSONResponse {
	return unprocessable(ErrorCodeCatalogNotManaged, "this catalog belongs to its engine; Baleia does not change it")
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

// bindingSyncStatus is the derived status of a binding (binding_status_v)
// and, when the engine refused to load an active binding's catalog, its
// message.
func (h *core) bindingSyncStatus(ctx context.Context, b db.CatalogBinding) (status, engineError string) {
	st, err := h.queries.GetBindingStatus(ctx, b.ID)
	if err != nil || st.SyncStatus == "" {
		return "pending", ""
	}
	return st.SyncStatus, st.EngineError
}

// engineLoadFailed is the error code of a binding the engine refused to load.
const engineLoadFailed = "engine_load_failed"

// catalogResponse is a catalog with its primary binding's status.
func (h *core) catalogResponse(ctx context.Context, cat db.Catalog, b db.CatalogBinding, version string) Catalog {
	c := dbCatalogToPublic(cat)
	if acts := h.catalogActions(ctx, cat.ID); acts != nil {
		c.AllowedActions = &acts
	}
	st, engineErr := h.bindingSyncStatus(ctx, b)
	status := CatalogSyncStatus(st)
	c.SyncStatus = &status
	c.SyncError = b.LastErrorMessage
	if engineErr != "" {
		c.SyncError = &engineErr
	}
	if version != "" && status == CatalogSyncStatusSynced {
		c.CatalogVersion = &version
	}
	h.metadataFreshness(ctx, cat.ID, &c)
	return c
}

// metadataFreshness fills a catalog's metadata sync fields from its latest
// passes (#17). A read error leaves them unset rather than failing the
// response.
func (h *core) metadataFreshness(ctx context.Context, id pgtype.UUID, c *Catalog) {
	st, err := h.queries.GetCatalogSyncState(ctx, id)
	if err != nil {
		slog.Warn("catalog metadata freshness", "err", err)
		return
	}
	status := CatalogMetadataSyncStatusNever
	if st.LastStatus != "" {
		status = CatalogMetadataSyncStatus(st.LastStatus)
	}
	c.MetadataSyncStatus = &status
	if st.LastStatus == "failed" && st.LastErrorCode != "" {
		code := st.LastErrorCode
		c.MetadataSyncErrorCode = &code
	}
	if st.SyncedAt.Valid {
		t := st.SyncedAt.Time
		c.MetadataSyncedAt = &t
	}
}

// bindingResponse is a binding with its derived status and lifecycle.
func (h *core) bindingResponse(ctx context.Context, b db.CatalogBinding, version string) CatalogBinding {
	out := bindingToPublic(b)
	if acts := h.catalogBindingActions(ctx, dbCatalogBindingRef{catalogID: b.CatalogID}); acts != nil {
		out.AllowedActions = &acts
	}
	st, engineErr := h.bindingSyncStatus(ctx, b)
	status := CatalogBindingSyncStatus(st)
	out.SyncStatus = &status
	if engineErr != "" {
		code := engineLoadFailed
		out.LastErrorCode, out.LastErrorMessage = &code, &engineErr
	}
	if version != "" && status == CatalogBindingSyncStatusSynced {
		out.Version = &version
	}
	return out
}

// catalogWithStatus is a stored catalog with its primary binding's status,
// or the bare catalog when it has none (native catalogs).
func (h *core) catalogWithStatus(ctx context.Context, cat db.Catalog) Catalog {
	bare := func() Catalog {
		c := dbCatalogToPublic(cat)
		if acts := h.catalogActions(ctx, cat.ID); acts != nil {
			c.AllowedActions = &acts
		}
		h.metadataFreshness(ctx, cat.ID, &c)
		return c
	}
	if cat.Origin != "source" || !cat.ClusterID.Valid {
		return bare()
	}
	_, eng, err := h.catalogEngine(ctx, cat)
	if err != nil {
		return bare()
	}
	b, err := h.queries.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: cat.ID, EngineID: eng.ID})
	if err != nil {
		return bare()
	}
	c := h.catalogResponse(ctx, cat, b, "")
	c.CatalogVersion = b.DesiredVersion
	return c
}
