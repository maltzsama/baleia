// Package providers builds the registry of every provider. It is called only
// at the wiring sites (docs/adr/0007).
package providers

import (
	"github.com/maltzsama/baleia/internal/connections"
	"github.com/maltzsama/baleia/internal/connections/clickhouse"
	"github.com/maltzsama/baleia/internal/connections/flightsql"
	"github.com/maltzsama/baleia/internal/connections/glue"
	"github.com/maltzsama/baleia/internal/connections/hive"
	"github.com/maltzsama/baleia/internal/connections/iceberg"
	"github.com/maltzsama/baleia/internal/connections/kafka"
	"github.com/maltzsama/baleia/internal/connections/lakekeeper"
	"github.com/maltzsama/baleia/internal/connections/mysql"
	"github.com/maltzsama/baleia/internal/connections/nessie"
	"github.com/maltzsama/baleia/internal/connections/oracle"
	"github.com/maltzsama/baleia/internal/connections/planned"
	"github.com/maltzsama/baleia/internal/connections/polaris"
	"github.com/maltzsama/baleia/internal/connections/postgresql"
	"github.com/maltzsama/baleia/internal/connections/sqlite"
	"github.com/maltzsama/baleia/internal/connections/sqlserver"
	"github.com/maltzsama/baleia/internal/connections/synthetic"
	"github.com/maltzsama/baleia/internal/connections/trino"
	"github.com/maltzsama/baleia/internal/connections/unity"
	"github.com/maltzsama/baleia/internal/dialect"
)

// Options are server-side settings of providers that need them.
type Options struct {
	SQLite sqlite.Options
	Glue   glue.Options
	// Secrets resolves external secret references in every Config the
	// registry builds (BALEIA_SECRET_FILE_BASE_DIR for file:).
	Secrets connections.SecretResolver
	// Dialects quote SQL for providers that read through an engine (Trino);
	// nil builds a registry of its own.
	Dialects *dialect.Registry
}

// New returns the registry of every provider, all dialing through d.
func New(d *connections.Dialer, opts ...Options) (*connections.Registry, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Dialects == nil {
		o.Dialects = dialect.NewRegistry()
	}
	all := []connections.Provider{
		polaris.New(d),
		nessie.New(d),
		iceberg.New(d),
		hive.New(d),
		glue.New(d, o.Glue),
		lakekeeper.New(d),
		unity.New(d),
		postgresql.New(d),
		mysql.New(d),
		mysql.NewMariaDB(d),
		sqlserver.New(d),
		oracle.New(d),
		sqlite.New(o.SQLite),
		clickhouse.New(d),
		flightsql.New(d),
		trino.New(d, o.Dialects.MustGet(dialect.Trino)),
		kafka.New(d),
	}
	all = append(all, planned.All()...)
	all = append(all, synthetic.All()...)
	reg, err := connections.NewRegistry(all...)
	if err != nil {
		return nil, err
	}
	return reg.WithSecrets(o.Secrets), nil
}
