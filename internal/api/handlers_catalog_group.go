package api

import (
	"context"

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs/admin"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
)

// CatalogHandlers serves connections, catalogs, engines, bindings and their metadata (#365).
type CatalogHandlers struct {
	*core
	// binder binds catalogs into Trino as the admin identity (docs/adr/0005,
	// #80), built at the wiring site.
	binder *bindtrino.Binder
	// engineAdmin stores engines and their native catalogs (#364).
	engineAdmin *manage.Service
	// catalogAdmin renames and retires catalogs and counts what depends on
	// them (#360).
	catalogAdmin *admin.Service
}

func newCatalogHandlers(c *core, binder *bindtrino.Binder) *CatalogHandlers {
	tx := func(ctx context.Context, fn func(manage.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	catalogTx := func(ctx context.Context, fn func(admin.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	return &CatalogHandlers{core: c, binder: binder, engineAdmin: manage.New(c.queries, tx), catalogAdmin: admin.New(c.queries, catalogTx)}
}
