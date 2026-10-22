// Package postgresql is the PostgreSQL direct engine (#82): Baleia runs SQL on
// a PostgreSQL connection itself, without Trino. A session is one dedicated
// pool connection; results stream as Arrow, mapped from the pgx field
// descriptions. Every dial goes through connections.Dialer (docs/adr/0011).
package postgresql

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind is the registered engine kind.
const Kind = "postgresql"

const (
	connectTimeout = 10 * time.Second
	// maxConns bounds the pool; one connection is held per session.
	maxConns = 8
)

// Spec is the kind's entry for a queryengine.Registry (#364).
func Spec() queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine}
}

// newEngine builds the engine from its connection config (host, port, database,
// sslmode, username; password in secrets). It dials through d.
func newEngine(ctx context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
	pc, err := poolConfig(cfg, d)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	return &Engine{id: row.ID, database: database(cfg), pool: pool}, nil
}

// poolConfig builds a pgxpool config from quoted keyword/value pairs, so no
// value can inject extra connection parameters; the password is set afterwards
// and never enters the string.
func poolConfig(cfg connections.Config, d *connections.Dialer) (*pgxpool.Config, error) {
	dsn := strings.Join([]string{
		"host=" + quote(cfg.Value("host")),
		"port=" + quote(valueOr(cfg.Value("port"), "5432")),
		"dbname=" + quote(database(cfg)),
		"user=" + quote(cfg.Value("username")),
		"sslmode=" + quote(sslMode(cfg)),
	}, " ")
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cc := pc.ConnConfig
	cc.Password = cfg.Secret("password")
	cc.ConnectTimeout = connectTimeout
	cc.DialFunc = d.DialContext
	// Resolution happens in the dialer, on the address it dials.
	cc.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	// The extended protocol without a prepared-statement cache: a statement is
	// one statement (multi-statement strings are rejected) and DISCARD ALL on
	// Close cannot invalidate a cached plan.
	cc.DefaultQueryExecMode = pgx.QueryExecModeExec
	cc.RuntimeParams["application_name"] = "baleia"
	pc.MaxConns = maxConns
	return pc, nil
}

// Engine is a PostgreSQL server reached directly.
type Engine struct {
	id       string
	database string
	pool     *pgxpool.Pool
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.PostgreSQL) }
func (e *Engine) Close() error                 { e.pool.Close(); return nil }

// Capabilities of PostgreSQL in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:             dialect.PostgreSQL,
		IdentityModes:       []string{"connection"},
		ReadOnly:            queryengine.ReadOnlyEnforced,
		CancelScope:         queryengine.CancelAnyProcess,
		SupportsCancel:      true,
		Transactions:        true,
		MultipleResultSets:  false,
		MultipleCatalogs:    false, // a session cannot switch database
		NamespaceDepth:      1,
		SessionSchema:       true,
		SupportsExplain:     true,
		MaxIdentifierLength: 63,
		IdentifierCase:      "lower",
	}
}

// Version reports the server version.
func (e *Engine) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.pool.QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", mapError(err)
	}
	return v, nil
}

// Ping fails while the server is unreachable.
func (e *Engine) Ping(ctx context.Context) error {
	if err := e.pool.Ping(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// Cancel cancels a running statement from any process with pg_cancel_backend,
// using the handle's backend pid. A statement that is gone is not an error
// (pg_cancel_backend returns false).
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	pid, err := backendPID(h)
	if err != nil {
		return err
	}
	var cancelled bool
	if err := e.pool.QueryRow(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&cancelled); err != nil {
		return mapError(err)
	}
	return nil
}

func (e *Engine) OpenSession(ctx context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	s := &session{engine: e, conn: conn, spec: spec, readOnly: spec.ReadOnly}
	if err := s.setup(ctx); err != nil {
		conn.Release()
		return nil, err
	}
	return s, nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func database(cfg connections.Config) string { return valueOr(cfg.Value("database"), "postgres") }

func sslMode(cfg connections.Config) string { return valueOr(cfg.Value("sslmode"), "prefer") }

// quote renders a value for a keyword/value connection string.
func quote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}
