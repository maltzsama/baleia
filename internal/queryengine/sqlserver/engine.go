// Package sqlserver is the SQL Server direct engine (#81, PR 1): Baleia runs
// SQL on a SQL Server connection itself, without Trino. A session is one
// dedicated pool connection; each batch streams as Arrow, mapped from the
// driver's column types. Every dial goes through connections.Dialer.
package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	mssql "github.com/microsoft/go-mssqldb"
)

// Kind is the registered engine kind.
const Kind = "sqlserver"

const connectTimeout = 10 * time.Second

// Spec is the kind's entry for a queryengine.Registry (#364).
func Spec() queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine}
}

// newEngine builds the engine from its connection config. It dials through d.
func newEngine(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
	connector, err := connector(cfg, d)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(time.Minute)
	return &Engine{
		id:             row.ID,
		database:       valueOr(cfg.Value("database"), "master"),
		cancelWithKill: cfg.Value("cancel_with_kill") == "true",
		db:             db,
	}, nil
}

// connector builds a URL-form DSN (values escaped by net/url) and hooks the
// dialer into the connector.
func connector(cfg connections.Config, d *connections.Dialer) (*mssql.Connector, error) {
	q := url.Values{}
	q.Set("database", valueOr(cfg.Value("database"), "master"))
	q.Set("encrypt", valueOr(cfg.Value("encrypt"), "true"))
	q.Set("TrustServerCertificate", valueOr(cfg.Value("trust_server_certificate"), "false"))
	q.Set("app name", "baleia")
	q.Set("dial timeout", fmt.Sprint(int(connectTimeout.Seconds())))
	u := &url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.Value("username"), cfg.Secret("password")),
		Host:     net.JoinHostPort(cfg.Value("host"), valueOr(cfg.Value("port"), "1433")),
		RawQuery: q.Encode(),
	}
	c, err := mssql.NewConnector(u.String())
	if err != nil {
		return nil, fmt.Errorf("sqlserver config: %w", connections.ErrValidation)
	}
	c.Dialer = d
	return c, nil
}

// Engine is a SQL Server instance reached directly.
type Engine struct {
	id             string
	database       string
	cancelWithKill bool
	db             *sql.DB
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.TSQL) }
func (e *Engine) Close() error                 { return e.db.Close() }

// Capabilities of SQL Server in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	scope := queryengine.CancelInProcess
	if e.cancelWithKill {
		scope = queryengine.CancelAnyProcess
	}
	return queryengine.Capabilities{
		Dialect:       dialect.TSQL,
		IdentityModes: []string{"connection"},
		// ApplicationIntent=ReadOnly is availability-group routing, not
		// enforcement: a read grant needs the credentials_read_only assertion.
		ReadOnly:            queryengine.ReadOnlyNone,
		CancelScope:         scope,
		SupportsCancel:      true,
		Transactions:        true,
		MultipleResultSets:  true,
		MultipleCatalogs:    true, // three-part names reach other databases
		NamespaceDepth:      1,
		SessionSchema:       false, // unqualified names use the login's default schema
		SupportsExplain:     true,
		MaxIdentifierLength: 128,
		IdentifierCase:      "preserve",
	}
}

// Version reports the server version.
func (e *Engine) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.db.QueryRowContext(ctx, "SELECT CONVERT(varchar(128), SERVERPROPERTY('ProductVersion'))").Scan(&v); err != nil {
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

// Cancel kills a running statement with KILL <spid> when the connection asserts
// the ALTER ANY CONNECTION permission (cancel_with_kill); otherwise SQL Server
// can only be cancelled in-process, through the statement's context.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if !e.cancelWithKill {
		return queryengine.ErrCancelUnsupported
	}
	spid, err := sessionID(h)
	if err != nil {
		return err
	}
	if _, err := e.db.ExecContext(ctx, "KILL "+strconv.Itoa(spid)); err != nil {
		if isNotRunning(err) {
			return nil // already finished: not an error
		}
		return mapError(err)
	}
	return nil
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

// quoteIdent brackets a name, doubling closing brackets.
func quoteIdent(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }
