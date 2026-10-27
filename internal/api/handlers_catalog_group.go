package api

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs/admin"
	"github.com/galileostudio/baleia/internal/catalogs/conns"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
)

// CatalogHandlers serves connections, catalogs, engines, bindings and their metadata (#365).
type CatalogHandlers struct {
	// connectionTests rate-limits connection and engine tests per user;
	// one per API, never a package global (parallel tests raced on one).
	connectionTests *testLimiter
	// ddlTimeout bounds each catalog DDL (catalogDDLTimeout; tests shorten it
	// on their own instance).
	ddlTimeout time.Duration
	*core
	// binder binds catalogs into Trino as the admin identity (docs/adr/0005,
	// #80), built at the wiring site.
	binder *bindtrino.Binder
	// engineAdmin stores engines and their native catalogs (#364).
	engineAdmin *manage.Service
	// catalogAdmin renames and retires catalogs and counts what depends on
	// them (#360).
	catalogAdmin *admin.Service
	// conns stores catalog connections (#360).
	conns *conns.Service
	// stores are the parts of the database the binding procedures,
	// reconcile and Hive redirection use, injected here once.
	stores catalogStores
}

// catalogStores hands each binding package the store it declares.
type catalogStores struct {
	lifecycle bindings.LifecycleStore
	registry  bindtrino.RegistryStore
	reconcile bindings.ReconcileStore
	projector stored.Store
	runs      stored.RunStore
	redirect  bindings.RedirectionStore
	primary   primaryCatalogStore
}

// primaryCatalogStore mirrors a primary binding onto its catalog.
type primaryCatalogStore interface {
	UpdateCatalog(ctx context.Context, arg db.UpdateCatalogParams) (db.Catalog, error)
	SetCatalogEnabled(ctx context.Context, arg db.SetCatalogEnabledParams) error
}

func newCatalogHandlers(c *core, binder *bindtrino.Binder) *CatalogHandlers {
	tx := func(ctx context.Context, fn func(manage.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	catalogTx := func(ctx context.Context, fn func(admin.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	q := c.queries
	return &CatalogHandlers{
		core: c, binder: binder, engineAdmin: manage.New(q, tx), catalogAdmin: admin.New(q, catalogTx), conns: conns.New(q),
		stores:          catalogStores{lifecycle: q, registry: q, reconcile: q, projector: q, runs: q, redirect: q, primary: q},
		connectionTests: newTestLimiter(),
		ddlTimeout:      catalogDDLTimeout,
	}
}
