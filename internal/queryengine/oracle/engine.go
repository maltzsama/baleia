// Package oracle is the Oracle Database direct engine (#81, PR 2): Baleia runs
// SQL on an Oracle connection itself, without Trino. A session is one dedicated
// pool connection; results stream as Arrow, mapped from the driver's column
// types. Every dial goes through connections.Dialer (OracleConnector.Dialer).
package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	goora "github.com/sijms/go-ora/v2"
)

// Kind is the registered engine kind.
const Kind = "oracle"

const (
	connectTimeout = 10 * time.Second
	// defaultLobMaxBytes caps a CLOB/NCLOB/BLOB value read into Arrow.
	defaultLobMaxBytes = 1 << 20
)

// Options are the server-side Oracle settings.
type Options struct {
	// LOBMaxBytes caps a CLOB/NCLOB/BLOB value read into Arrow
	// (BALEIA_LOB_MAX_BYTES); zero or less means 1 MiB.
	LOBMaxBytes int
}

func (o Options) lobMaxBytes() int {
	if o.LOBMaxBytes > 0 {
		return o.LOBMaxBytes
	}
	return defaultLobMaxBytes
}

// Spec is the kind's entry for a queryengine.Registry (#364).
func Spec(o Options) queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine(o)}
}

// newEngine returns the factory: it builds the engine from its connection
// config and dials through d.
func newEngine(o Options) queryengine.Factory {
	return func(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		return buildEngine(row, cfg, d, o)
	}
}

func buildEngine(row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer, o Options) (queryengine.Engine, error) {
	port, err := strconv.Atoi(valueOr(cfg.Value("port"), "1521"))
	if err != nil {
		return nil, fmt.Errorf("oracle config: %w", connections.ErrValidation)
	}
	dsn := goora.BuildUrl(cfg.Value("host"), port, cfg.Value("service_name"), cfg.Value("username"), cfg.Secret("password"),
		map[string]string{"CONNECTION TIMEOUT": fmt.Sprint(int(connectTimeout.Seconds())), "PROGRAM": "baleia"})
	c, ok := goora.NewConnector(dsn).(*goora.OracleConnector)
	if !ok {
		return nil, errors.New("oracle: unexpected connector type")
	}
	c.Dialer(d)
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(time.Minute)
	return &Engine{
		id:                    row.ID,
		catalog:               cfg.Value("service_name"),
		cancelWithAlterSystem: cfg.Value("cancel_with_alter_system") == "true",
		lobMaxBytes:           o.lobMaxBytes(),
		db:                    db,
	}, nil
}

// Engine is an Oracle Database reached directly.
type Engine struct {
	id                    string
	catalog               string
	cancelWithAlterSystem bool
	lobMaxBytes           int
	db                    *sql.DB
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.Oracle) }
func (e *Engine) Close() error                 { return e.db.Close() }

// Capabilities of Oracle in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	scope := queryengine.CancelInProcess
	if e.cancelWithAlterSystem {
		scope = queryengine.CancelAnyProcess
	}
	return queryengine.Capabilities{
		Dialect:       dialect.Oracle,
		IdentityModes: []string{"connection"},
		// DDL commits implicitly and SET TRANSACTION READ ONLY does not stop it:
		// a read grant needs the credentials_read_only assertion (#20).
		ReadOnly:            queryengine.ReadOnlyNone,
		CancelScope:         scope,
		SupportsCancel:      true,
		Transactions:        false, // no client-side BEGIN; DML is implicit
		MultipleResultSets:  false,
		MultipleCatalogs:    false, // one catalog: the service the connection opened
		NamespaceDepth:      1,
		SessionSchema:       true,
		SupportsExplain:     false,
		MaxIdentifierLength: 128,
		IdentifierCase:      "upper",
	}
}

// Version reports the database version.
func (e *Engine) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.db.QueryRowContext(ctx, "SELECT version FROM product_component_version WHERE ROWNUM = 1").Scan(&v); err != nil {
		return "", mapError(err)
	}
	return v, nil
}

// Ping fails while the database is unreachable.
func (e *Engine) Ping(ctx context.Context) error {
	if err := e.db.PingContext(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// Cancel cancels a running statement with ALTER SYSTEM CANCEL SQL when the
// connection asserts that permission (cancel_with_alter_system); otherwise
// Oracle can only be cancelled in-process, through the statement's context.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if !e.cancelWithAlterSystem {
		return queryengine.ErrCancelUnsupported
	}
	sid, serial, err := sessionIDs(h)
	if err != nil {
		return err
	}
	if _, err := e.db.ExecContext(ctx, fmt.Sprintf("ALTER SYSTEM CANCEL SQL '%d, %d'", sid, serial)); err != nil {
		if isNotRunning(err) {
			return nil
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
