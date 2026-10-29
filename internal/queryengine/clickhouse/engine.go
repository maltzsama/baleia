// Package clickhouse is the ClickHouse direct engine (#83): Baleia runs SQL on
// a ClickHouse server itself, without Trino, over the HTTP interface. Results
// arrive as Arrow natively (FORMAT ArrowStream) and stream straight into the
// sink, with no type mapping in Baleia. Every request goes through
// connections.Dialer; credentials travel in headers, never in the URL.
package clickhouse

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Kind is the registered engine kind.
const Kind = "clickhouse"

// Spec is the kind's entry for a queryengine.Registry (#364).
func Spec(dl queryengine.Dialect) queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassDirect, Factory: newEngine(dl)}
}

// newEngine builds the engine from its connection config (host, port, secure,
// database, username; password in secrets). Every dial goes through d.
func newEngine(dl queryengine.Dialect) queryengine.Factory {
	return func(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		if cfg.Value("host") == "" {
			return nil, queryengine.NewError(queryengine.CodeUnavailable, Kind, "", "connection has no host")
		}
		return &Engine{dialect: dl, id: row.ID, c: newClient(cfg, d)}, nil
	}
}

// Engine is a ClickHouse server reached over HTTP.
type Engine struct {
	dialect queryengine.Dialect
	id      string
	c       *client
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return e.dialect }
func (e *Engine) Close() error                 { return nil }

// Capabilities of ClickHouse in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:        dialect.ClickHouse,
		IdentityModes:  []string{"connection"},
		ReadOnly:       queryengine.ReadOnlyEnforced, // readonly=2 on every request of the session
		CancelScope:    queryengine.CancelAnyProcess, // KILL QUERY by query_id from another process
		SupportsCancel: true,
		// ClickHouse has no transactions over the HTTP interface.
		Transactions:       false,
		MultipleResultSets: false,
		MultipleCatalogs:   false, // one server is one catalog
		NamespaceDepth:     1,     // namespaces are the databases
		SessionSchema:      true,  // USE persists in the HTTP session
		SupportsExplain:    true,
		IdentifierCase:     "preserve",
	}
}

// Version reports the server version.
func (e *Engine) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	rows, err := e.c.queryStrings(ctx, request{sql: "SELECT version()"})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return "", queryengine.NewError(queryengine.CodeInternal, Kind, "", "no version returned")
	}
	return rows[0][0], nil
}

// Ping fails while the server is unreachable.
func (e *Engine) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	_, err := e.c.queryStrings(ctx, request{sql: "SELECT 1"})
	return err
}

// Cancel kills a running statement from any process with KILL QUERY on the
// handle's query id, passed as a query parameter (never interpolated). A
// statement that is gone is not an error.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if h.QueryID == "" {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	return queryengine.CancelUntilFound(queryengine.CancelWindow, func(left time.Duration) (bool, error) {
		kctx, cancel := context.WithTimeout(ctx, min(connectTimeout, left))
		defer cancel()
		rows, err := e.c.queryStrings(kctx, request{
			sql:    "KILL QUERY WHERE query_id = {id:String} SYNC",
			params: map[string]string{"id": h.QueryID},
		})
		return len(rows) > 0, err
	})
}

// OpenSession starts one ClickHouse HTTP session; USE and SET persist on it.
func (e *Engine) OpenSession(ctx context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	s := newSession(e, spec)
	if len(spec.Schema) > 0 {
		db := spec.Schema[len(spec.Schema)-1]
		q, err := e.Dialect().QuoteIdent(db)
		if err != nil {
			return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
		}
		if err := s.exec(ctx, "USE "+q); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }
