// Package providers builds the registry of every provider. It is called only
// at the wiring sites (docs/adr/0007).
package providers

import (
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/iceberg"
	"github.com/galileostudio/baleia/internal/connections/mysql"
	"github.com/galileostudio/baleia/internal/connections/nessie"
	"github.com/galileostudio/baleia/internal/connections/polaris"
	"github.com/galileostudio/baleia/internal/connections/postgresql"
)

// New returns the registry of every provider, all dialing through d.
func New(d *connections.Dialer) (*connections.Registry, error) {
	return connections.NewRegistry(
		polaris.New(d),
		nessie.New(d),
		iceberg.New(d),
		postgresql.New(d),
		mysql.New(d),
	)
}
