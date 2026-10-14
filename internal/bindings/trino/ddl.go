// Package trino binds catalogs into Trino through the catalog store plugin
// (docs/adr/0003). This file holds the catalog DDL the handlers use today;
// the binder contract around it is #80.
package trino

import (
	"context"

	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// CatalogSpec describes a catalog to create with CREATE CATALOG; secrets in
// SecretKeys become @baleia-secret[...] placeholders in the DDL text.
type CatalogSpec = trinoclient.CatalogSpec

// ErrCatalogExists is returned by CreateCatalog for a name already in use.
var ErrCatalogExists = trinoclient.ErrCatalogExists

// DDL runs catalog DDL as the admin identity (the only identity the catalog
// guard lets manage catalogs, docs/adr/0005).
type DDL struct{ c *trinoclient.Client }

// NewDDL wraps an admin client built at a wiring site.
func NewDDL(admin *trinoclient.Client) *DDL { return &DDL{c: admin} }

// CreateCatalog registers a catalog at runtime.
func (d *DDL) CreateCatalog(ctx context.Context, spec CatalogSpec) error {
	return d.c.CreateCatalog(ctx, spec)
}

// DropCatalog removes a runtime-registered catalog.
func (d *DDL) DropCatalog(ctx context.Context, name string) error { return d.c.DropCatalog(ctx, name) }

// ListCatalogs returns the catalogs Trino has loaded.
func (d *DDL) ListCatalogs(ctx context.Context) ([]string, error) { return d.c.ListCatalogs(ctx) }
