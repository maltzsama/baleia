// Package mysql is the MySQL/MariaDB direct engine (#82, PR 2): Baleia runs SQL
// on a MySQL or MariaDB connection itself, without Trino. A session is one
// dedicated pool connection; results stream as Arrow, mapped from the driver's
// column types. Every dial goes through connections.Dialer (docs/adr/0011).
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	driver "github.com/go-sql-driver/mysql"
)

// Registered engine kinds.
const (
	KindMySQL   = "mysql"
	KindMariaDB = "mariadb"
)

const connectTimeout = 10 * time.Second

func init() {
	queryengine.RegisterKind(KindMySQL, queryengine.ClassDirect, newEngine(false))
	queryengine.RegisterKind(KindMariaDB, queryengine.ClassDirect, newEngine(true))
}

// newEngine returns the factory of a MySQL or MariaDB engine.
func newEngine(mariadb bool) queryengine.Factory {
	kind := KindMySQL
	if mariadb {
		kind = KindMariaDB
	}
	return func(ctx context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		network := registerDialer(d)
		mc, err := driverConfig(cfg, network)
		if err != nil {
			return nil, err
		}
		connector, err := driver.NewConnector(mc)
		if err != nil {
			return nil, fmt.Errorf("mysql config: %w", connections.ErrValidation)
		}
		db := sql.OpenDB(connector)
		db.SetMaxOpenConns(8)
		db.SetConnMaxIdleTime(time.Minute)
		return &Engine{id: row.ID, kind: kind, mariadb: mariadb, db: db}, nil
	}
}

// namedNetworks registers the dialer as a named network once per dialer: the
// driver panics on a duplicate network name.
var namedNetworks = struct {
	sync.Mutex
	m map[*connections.Dialer]string
}{m: map[*connections.Dialer]string{}}

func registerDialer(d *connections.Dialer) string {
	namedNetworks.Lock()
	defer namedNetworks.Unlock()
	if n, ok := namedNetworks.m[d]; ok {
		return n
	}
	n := fmt.Sprintf("baleia-engine-%p", d)
	driver.RegisterDialContext(n, func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	})
	namedNetworks.m[d] = n
	return n
}

// driverConfig builds the mysql driver config from a connection's settings. No
// value can inject extra parameters: the driver config is built field by field.
func driverConfig(cfg connections.Config, network string) (*driver.Config, error) {
	mc := driver.NewConfig()
	mc.Net = network
	mc.Addr = net.JoinHostPort(cfg.Value("host"), valueOr(cfg.Value("port"), "3306"))
	mc.User = cfg.Value("username")
	mc.Passwd = cfg.Secret("password")
	mc.DBName = cfg.Value("database")
	mc.TLSConfig = valueOr(cfg.Value("tls"), "preferred")
	mc.ParseTime = true
	mc.InterpolateParams = false
	mc.MultiStatements = false
	mc.AllowAllFiles = false
	mc.AllowCleartextPasswords = false
	mc.Timeout = connectTimeout
	mc.ConnectionAttributes = "program_name:baleia"
	return mc, nil
}

// Engine is a MySQL or MariaDB server reached directly.
type Engine struct {
	id      string
	kind    string
	mariadb bool
	db      *sql.DB
}

func (e *Engine) ID() string               { return e.id }
func (e *Engine) Kind() string             { return e.kind }
func (e *Engine) Class() queryengine.Class { return queryengine.ClassDirect }
func (e *Engine) Close() error             { return e.db.Close() }

func (e *Engine) Dialect() queryengine.Dialect {
	if e.mariadb {
		return dialect.MustGet(dialect.MariaDB)
	}
	return dialect.MustGet(dialect.MySQL)
}

// Capabilities of MySQL/MariaDB in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:       e.Dialect().Name(),
		IdentityModes: []string{"connection"},
		// MySQL DDL commits implicitly and escapes START TRANSACTION READ ONLY;
		// read grants and agents need the credentials_read_only assertion (#20).
		ReadOnly:            queryengine.ReadOnlyNone,
		CancelScope:         queryengine.CancelAnyProcess,
		SupportsCancel:      true,
		Transactions:        true,
		MultipleResultSets:  false,
		MultipleCatalogs:    false,
		NamespaceDepth:      1,
		SessionSchema:       true,
		SupportsExplain:     true,
		MaxIdentifierLength: 64,
		IdentifierCase:      "preserve",
	}
}

// Version reports the server version; MariaDB is detected by its suffix.
func (e *Engine) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return "", mapError(err)
	}
	return v, nil
}

// Ping fails while the server is unreachable.
func (e *Engine) Ping(ctx context.Context) error {
	if err := e.db.PingContext(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// Cancel kills a running statement from any process with KILL QUERY on the
// handle's connection id. A statement that is gone is not an error.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	id, err := connectionID(h)
	if err != nil {
		return err
	}
	if err := e.kill(ctx, id); err != nil {
		return mapError(err)
	}
	return nil
}

// kill runs KILL QUERY from a pool connection other than the session's own.
func (e *Engine) kill(ctx context.Context, id uint64) error {
	_, err := e.db.ExecContext(ctx, "KILL QUERY ?", id)
	if isUnknownThread(err) {
		return nil
	}
	return err
}

func (e *Engine) OpenSession(ctx context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	s := &session{engine: e, conn: conn, spec: spec}
	if err := s.setup(ctx); err != nil {
		_ = conn.Close()
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
