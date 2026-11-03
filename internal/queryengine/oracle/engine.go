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
	"regexp"
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
func Spec(o Options, dl queryengine.Dialect) queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine(o, dl)}
}

// newEngine returns the factory: it builds the engine from its connection
// config and dials through d.
func newEngine(o Options, dl queryengine.Dialect) queryengine.Factory {
	return func(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		return buildEngine(row, cfg, d, o, dl)
	}
}

func buildEngine(row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer, o Options, dl queryengine.Dialect) (queryengine.Engine, error) {
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
		dialect:               dl,
		id:                    row.ID,
		catalog:               cfg.Value("service_name"),
		cancelWithAlterSystem: cfg.Value("cancel_with_alter_system") == "true",
		lobMaxBytes:           o.lobMaxBytes(),
		db:                    db,
	}, nil
}

// Engine is an Oracle Database reached directly.
type Engine struct {
	dialect               queryengine.Dialect
	id                    string
	catalog               string
	cancelWithAlterSystem bool
	lobMaxBytes           int
	db                    *sql.DB
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return e.dialect }
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
//
// A CANCEL SQL on an idle session stays pending and stops the session's next
// call. The handle is reported before the statement reaches the session
// (#505), and a cancel can also arrive after it ended: the pending cancel
// would then stop whatever the pooled connection runs next. So Cancel looks
// the tagged statement up in v$session and v$sql and names its sql_id, which
// stops that statement and no other, retrying until it is running. A handle
// without a tag (an older worker) cancels the session's current SQL.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if !e.cancelWithAlterSystem {
		return queryengine.ErrCancelUnsupported
	}
	sid, serial, err := sessionIDs(h)
	if err != nil {
		return err
	}
	tag := h.Extra[queryengine.StatementTagKey]
	if tag == "" {
		return e.cancelSQL(ctx, fmt.Sprintf("%d, %d", sid, serial))
	}
	ctx = context.WithoutCancel(ctx)
	return queryengine.CancelUntilFound(queryengine.CancelWindow, func(left time.Duration) (bool, error) {
		actx, cancel := context.WithTimeout(ctx, left)
		defer cancel()
		var sqlID string
		err := e.db.QueryRowContext(actx, `SELECT s.sql_id FROM v$session s
			JOIN v$sql q ON q.sql_id = s.sql_id AND q.child_number = s.sql_child_number
			WHERE s.sid = :1 AND s.serial# = :2 AND s.status = 'ACTIVE' AND q.sql_text LIKE :3`,
			sid, serial, queryengine.TagPattern(tag)).Scan(&sqlID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, mapError(err)
		}
		if !sqlIDPattern.MatchString(sqlID) {
			return false, queryengine.NewError(queryengine.CodeInternal, Kind, "", "unexpected sql_id")
		}
		return true, e.cancelSQL(actx, fmt.Sprintf("%d, %d, %s", sid, serial, sqlID))
	})
}

// sqlIDPattern is Oracle's 13-character sql_id, checked before it is written
// into ALTER SYSTEM CANCEL SQL (which takes no bind variables).
var sqlIDPattern = regexp.MustCompile(`^[0-9a-z]{13}$`)

// cancelSQL runs ALTER SYSTEM CANCEL SQL; a statement that is gone is not an
// error.
func (e *Engine) cancelSQL(ctx context.Context, target string) error {
	if _, err := e.db.ExecContext(ctx, "ALTER SYSTEM CANCEL SQL '"+target+"'"); err != nil {
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
