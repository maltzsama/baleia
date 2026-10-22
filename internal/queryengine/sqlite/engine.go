// Package sqlite is the SQLite direct engine (#83): Baleia runs SQL on a
// SQLite database file itself, without Trino. SQLite has no network: a session
// is one dedicated database/sql connection to a file resolved inside
// BALEIA_SQLITE_ROOT (docs/adr/0011), opened read-only unless both the
// connection and the server allow writes. Results stream as Arrow, mapped by
// the declared type affinity of each column (modernc.org/sqlite, pure Go).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	connectionssqlite "github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Kind is the registered engine kind.
const Kind = "sqlite"

// maxConns bounds each pool; SQLite allows concurrent readers.
const maxConns = 8

// Spec is the kind's entry for a queryengine.Registry (#364): engines it
// builds use o, the same confinement the SQLite provider applies.
func Spec(o Options) queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: func(_ context.Context, row queryengine.EngineRow, cfg connections.Config, _ *connections.Dialer) (queryengine.Engine, error) {
		return newEngineWithOptions(row, cfg, o)
	}}
}

// newEngineWithOptions builds the engine from its connection config (path,
// read_write) and the server-side options. SQLite dials nothing.
func newEngineWithOptions(row queryengine.EngineRow, cfg connections.Config, o Options) (queryengine.Engine, error) {
	if o.Root == "" {
		return nil, queryengine.NewError(queryengine.CodeUnavailable, Kind, "", "SQLite is not configured: BALEIA_SQLITE_ROOT is not set")
	}
	path, err := connectionssqlite.ResolvePath(o.Root, cfg.Value("path"))
	if err != nil {
		return nil, mapPathError(err)
	}
	// A read-only pool always exists; a read-write pool only when the
	// connection and the server both allow writes, so a read-only session is
	// enforced by the file mode itself (docs/adr/0011).
	ro, err := openPool(path, false)
	if err != nil {
		return nil, err
	}
	e := &Engine{id: row.ID, ro: ro}
	if connectionssqlite.WriteAllowed(o.AllowWrite, cfg) {
		rw, err := openPool(path, true)
		if err != nil {
			ro.Close()
			return nil, err
		}
		e.rw = rw
	}
	return e, nil
}

func openPool(path string, writable bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", connectionssqlite.DSN(path, writable))
	if err != nil {
		return nil, mapError(err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetConnMaxIdleTime(time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, mapError(err)
	}
	return db, nil
}

// Engine is a SQLite database file reached directly.
type Engine struct {
	id string
	ro *sql.DB
	rw *sql.DB // nil when the connection is read-only
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.SQLite) }

func (e *Engine) Close() error {
	err := e.ro.Close()
	if e.rw != nil {
		if rerr := e.rw.Close(); err == nil {
			err = rerr
		}
	}
	return err
}

// Capabilities of SQLite in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:        dialect.SQLite,
		IdentityModes:  []string{"connection"},
		ReadOnly:       queryengine.ReadOnlyEnforced, // read-only connection + refused statements
		CancelScope:    queryengine.CancelInProcess,  // SQLite is in-process; ctx → interrupt
		SupportsCancel: true,
		Transactions:   true,
		// NamespaceDepth 0: one database, no schemas; SessionSchema false.
		MultipleResultSets: false,
		MultipleCatalogs:   false,
		NamespaceDepth:     0,
		SessionSchema:      false,
		SupportsExplain:    true,
		IdentifierCase:     "preserve",
	}
}

// Ping fails while the file is unreadable.
func (e *Engine) Ping(ctx context.Context) error {
	if err := e.ro.PingContext(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// Cancel is unsupported by handle: a SQLite statement is interrupted by
// cancelling its Execute context, in this process (CancelScope in_process).
func (e *Engine) Cancel(context.Context, queryengine.QueryHandle) error {
	return queryengine.ErrCancelUnsupported
}

// OpenSession takes one dedicated connection; a read-only session uses the
// read-only pool even on a writable connection.
func (e *Engine) OpenSession(ctx context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	pool := e.ro
	if !spec.ReadOnly && e.rw != nil {
		pool = e.rw
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	s := &session{engine: e, conn: conn, spec: spec, readOnly: pool == e.ro}
	if err := s.setup(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }

// mapPathError turns a confinement failure into an engine error. A path that
// leaves the root is refused, not reported as a missing file.
func mapPathError(err error) error {
	switch {
	case errors.Is(err, connections.ErrNotFound):
		return queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	case errors.Is(err, connectionssqlite.ErrOutsideRoot):
		return queryengine.NewError(queryengine.CodePermission, Kind, "", err.Error())
	default:
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", err.Error())
	}
}
