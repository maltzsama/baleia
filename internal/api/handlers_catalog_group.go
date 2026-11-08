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
	"github.com/galileostudio/baleia/internal/store/repo"
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
	var (
		connStore    conns.Store  = c.queries
		engineStore  manage.Store = c.queries
		catalogStore admin.Store  = c.queries
		engineTx                  = func(ctx context.Context, fn func(manage.TxStore) error) error {
			return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
		}
		catalogTx = func(ctx context.Context, fn func(admin.TxStore) error) error {
			return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
		}
		stores = catalogStores{
			lifecycle: c.queries, registry: c.queries, reconcile: c.queries,
			projector: c.queries, runs: c.queries, redirect: c.queries, primary: c.queries,
		}
	)
	if c.store != nil {
		r := repo.New(c.store)
		connStore, engineStore, catalogStore = r, r, r
		engineTx = func(ctx context.Context, fn func(manage.TxStore) error) error {
			return r.TxEngines(ctx, fn)
		}
		catalogTx = func(ctx context.Context, fn func(admin.TxStore) error) error {
			return r.TxCatalogs(ctx, fn)
		}
		stores = catalogStores{
			lifecycle: r, registry: r, reconcile: r,
			projector: r, runs: r, redirect: r, primary: r,
		}
	}
	return &CatalogHandlers{
		core: c, binder: binder, engineAdmin: manage.New(engineStore, engineTx), catalogAdmin: admin.New(catalogStore, catalogTx),
		conns: conns.New(connStore), stores: stores,
		connectionTests: newTestLimiter(),
		ddlTimeout:      catalogDDLTimeout,
	}
}
