package api

import bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"

// CatalogHandlers serves connections, catalogs, engines, bindings and their metadata (#365).
type CatalogHandlers struct {
	*core
	// binder binds catalogs into Trino as the admin identity (docs/adr/0005,
	// #80), built at the wiring site.
	binder *bindtrino.Binder
}
