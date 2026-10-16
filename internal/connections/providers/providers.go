// Package providers builds the registry of every provider. It is called only
// at the wiring sites (docs/adr/0007).
package providers

import (
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/clickhouse"
	"github.com/galileostudio/baleia/internal/connections/flightsql"
	"github.com/galileostudio/baleia/internal/connections/glue"
	"github.com/galileostudio/baleia/internal/connections/hive"
	"github.com/galileostudio/baleia/internal/connections/iceberg"
	"github.com/galileostudio/baleia/internal/connections/mysql"
	"github.com/galileostudio/baleia/internal/connections/nessie"
	"github.com/galileostudio/baleia/internal/connections/oracle"
	"github.com/galileostudio/baleia/internal/connections/planned"
	"github.com/galileostudio/baleia/internal/connections/polaris"
	"github.com/galileostudio/baleia/internal/connections/postgresql"
	"github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/connections/sqlserver"
	"github.com/galileostudio/baleia/internal/connections/trino"
)

// Options are server-side settings of providers that need them.
type Options struct {
	SQLite sqlite.Options
	Glue   glue.Options
}

// New returns the registry of every provider, all dialing through d.
func New(d *connections.Dialer, opts ...Options) (*connections.Registry, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return connections.NewRegistry(
		polaris.New(d),
		nessie.New(d),
		iceberg.New(d),
		hive.New(d),
		glue.New(d, o.Glue),
		postgresql.New(d),
		mysql.New(d),
		mysql.NewMariaDB(d),
		sqlserver.New(d),
		oracle.New(d),
		sqlite.New(o.SQLite),
		clickhouse.New(d),
		flightsql.New(d),
		trino.New(d),
		planned.Doris(),
		planned.StarRocks(),
	)
}
