package flightsql

import (
	"context"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Kind is the registered engine kind.
const Kind = "flightsql"

func init() {
	queryengine.RegisterKind(Kind, queryengine.ClassDirect, newEngine)
}

// newEngine dials the Flight SQL endpoint. Every dial goes through d.
func newEngine(ctx context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
	if cfg.Value("host") == "" || cfg.Value("port") == "" {
		return nil, queryengine.NewError(queryengine.CodeUnavailable, Kind, "", "connection needs a host and a port")
	}
	c, err := openClient(ctx, cfg, d)
	if err != nil {
		return nil, mapError(err)
	}
	return &Engine{id: row.ID, c: c}, nil
}

// Engine is a Flight SQL endpoint reached directly.
type Engine struct {
	id string
	c  *client
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassDirect }
func (e *Engine) Dialect() queryengine.Dialect { return dialect.MustGet(dialect.FlightSQL) }
func (e *Engine) Close() error                 { return e.c.close() }

// Capabilities of Flight SQL in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:       dialect.FlightSQL,
		IdentityModes: []string{"connection"},
		// The protocol has no read-only session: a read-only target needs the
		// credentials_read_only assertion (docs/adr/0011, #20).
		ReadOnly:           queryengine.ReadOnlyNone,
		CancelScope:        queryengine.CancelInProcess, // FlightInfo tickets are bearer capabilities, not persisted
		SupportsCancel:     true,
		Transactions:       false,
		MultipleResultSets: false,
		MultipleCatalogs:   false,
		NamespaceDepth:     1,
		SessionSchema:      false, // sessions are mostly stateless; the dialect qualifies
		SupportsExplain:    false, // no single-statement EXPLAIN
		IdentifierCase:     "preserve",
	}
}

// Ping fails while the server is unreachable.
func (e *Engine) Ping(ctx context.Context) error {
	_, err := e.c.catalogs(ctx)
	return err
}

// Cancel by handle is unsupported: a FlightInfo ticket is a bearer capability
// and is never persisted, so a statement is cancelled by its context, in this
// process (CancelScope in_process).
func (e *Engine) Cancel(context.Context, queryengine.QueryHandle) error {
	return queryengine.ErrCancelUnsupported
}

// OpenSession starts a session. Flight SQL carries no server-side session
// state, so the session holds the SET/SHOW variables Baleia tracks locally.
func (e *Engine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	return &session{engine: e, spec: spec, vars: map[string]string{}}, nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }
