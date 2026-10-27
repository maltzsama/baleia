// Package engines lists Baleia's engine kinds (#364). It is the only package
// that knows every adapter: the composition root (internal/app) builds the
// registry here and injects it, instead of adapters registering themselves
// into a global from init().
package engines

import (
	"errors"
	"log/slog"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/clickhouse"
	"github.com/galileostudio/baleia/internal/queryengine/flightsql"
	"github.com/galileostudio/baleia/internal/queryengine/mysql"
	"github.com/galileostudio/baleia/internal/queryengine/oracle"
	"github.com/galileostudio/baleia/internal/queryengine/postgresql"
	"github.com/galileostudio/baleia/internal/queryengine/sqlite"
	"github.com/galileostudio/baleia/internal/queryengine/sqlserver"
	"github.com/galileostudio/baleia/internal/queryengine/trino"
)

// Options are the server-side settings some kinds need.
type Options struct {
	// SQLite confines database files (BALEIA_SQLITE_ROOT,
	// BALEIA_SQLITE_ALLOW_WRITE), like the SQLite provider.
	SQLite sqlite.Options
	Oracle oracle.Options
	// Dialects gives every kind the dialect it speaks.
	Dialects *dialect.Registry
	// Logger receives the engines' logs; nil discards.
	Logger *slog.Logger
}

// NewRegistry builds the registry of every engine kind Baleia ships.
func NewRegistry(o Options) (*queryengine.Registry, error) {
	if o.Dialects == nil {
		return nil, errors.New("engines: Options.Dialects is required")
	}
	d := o.Dialects
	specs := []queryengine.KindSpec{
		clickhouse.Spec(d.MustGet(dialect.ClickHouse)),
		flightsql.Spec(d.MustGet(dialect.FlightSQL)),
		oracle.Spec(o.Oracle, d.MustGet(dialect.Oracle)),
		postgresql.Spec(d.MustGet(dialect.PostgreSQL)),
		sqlite.Spec(o.SQLite, d.MustGet(dialect.SQLite)),
		sqlserver.Spec(d.MustGet(dialect.TSQL)),
		trino.Spec(d.MustGet(dialect.Trino), o.Logger),
	}
	specs = append(specs, mysql.Specs(d.MustGet(dialect.MySQL), d.MustGet(dialect.MariaDB))...)
	return queryengine.NewRegistry(specs...)
}
