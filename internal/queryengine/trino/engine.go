// Package trino is the Trino adapter of the engine contract (#25). Trino has
// no connections: a session is client-side state (catalog, schema, session
// properties, prepared statements) replayed on every request and updated
// from the X-Trino-Set-* response headers.
package trino

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// Kind is the registered engine kind.
const Kind = "trino"

// Default identities when the engine's connection config names none.
const (
	defaultServiceUser = "baleia"
	defaultAdminUser   = "baleia_admin"
)

// requestTimeout bounds one HTTP request (one page), not a statement.
const requestTimeout = 60 * time.Second

func init() {
	queryengine.RegisterKind(Kind, queryengine.ClassFederated, newEngine)
}

// newEngine builds the engine from its connection config: url, and the
// service_user (fallback identity) and admin_user (kills queries by handle)
// that bootstrap keeps in step with the environment.
func newEngine(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
	url := cfg.Value("url")
	if url == "" {
		return nil, fmt.Errorf("trino engine %s: connection has no url", row.Name)
	}
	service := valueOr(cfg.Value("service_user"), defaultServiceUser)
	admin := valueOr(cfg.Value("admin_user"), defaultAdminUser)
	hc := d.HTTPClient(requestTimeout)
	return &Engine{
		id:     row.ID,
		client: trinoclient.NewWithHTTPClient(url, service, hc),
		admin:  trinoclient.NewWithHTTPClient(url, admin, hc),
	}, nil
}

// New builds an engine for a coordinator URL; tests and tools use it.
func New(id, url, serviceUser, adminUser string, d *connections.Dialer) *Engine {
	hc := d.HTTPClient(requestTimeout)
	return &Engine{
		id:     id,
		client: trinoclient.NewWithHTTPClient(url, valueOr(serviceUser, defaultServiceUser), hc),
		admin:  trinoclient.NewWithHTTPClient(url, valueOr(adminUser, defaultAdminUser), hc),
	}
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Engine is a Trino coordinator.
type Engine struct {
	id     string
	client *trinoclient.Client // runs statements; identity set per session
	admin  *trinoclient.Client // kills queries by handle; never runs user SQL
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassFederated }
func (e *Engine) Dialect() queryengine.Dialect { return dialect{} }
func (e *Engine) Close() error                 { return nil }

// Capabilities of Trino in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		IdentityModes:      []string{"impersonation"},
		ReadOnly:           queryengine.ReadOnlyEnforced, // the catalog guard's read-only mode (#92)
		CancelScope:        queryengine.CancelAnyProcess,
		Transactions:       false,
		MultipleResultSets: false,
		Monitoring:         true,
		IcebergMaintenance: true,
	}
}

// Ping fails while the coordinator is down or still starting.
func (e *Engine) Ping(ctx context.Context) error {
	info, err := e.client.Info(ctx)
	if err != nil {
		return mapError(err)
	}
	if info.Starting {
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "SERVER_STARTING_UP", "trino is starting")
	}
	return nil
}

// Cancel kills a query by id with the admin identity, from any process. A
// query that already finished is not an error.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if h.QueryID == "" {
		return nil
	}
	err := e.admin.KillQuery(ctx, h.QueryID, "cancelled by baleia")
	var qe *trinoclient.QueryError
	if errors.As(err, &qe) && (qe.ErrorName == "NOT_FOUND" || strings.Contains(qe.Message, "is not running")) {
		return nil // already finished: not an error
	}
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (e *Engine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	return newSession(e, spec), nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }

type dialect struct{}

func (dialect) Name() string { return "trino" }
