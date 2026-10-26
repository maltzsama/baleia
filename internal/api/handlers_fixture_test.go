package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content/history"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// handlerFields are the dependencies a test sets. Tests build handlers only
// through testAPI, so splitting the handlers by domain (#365) changes
// this file, not every fixture.
type handlerFields struct {
	queries        db.Querier
	pool           *pgxpool.Pool
	tx             txRunner
	schema         schemaVersioner
	settings       Settings
	keyring        *crypto.Keyring
	temporalClient client.Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
	versionCache   *TTLStore[engineProbe]
	attentionCache *TTLStore[[]AttentionItem]
	connections    *connections.Registry
	engines        engineSource
	kinds          engineKinds
	dialects       *dialect.Registry
	binder         *bindtrino.Binder
	audit          audit.Recorder
	dialer         *connections.Dialer
}

// testAPI builds handlers from the fields a test sets; the rest stay zero.
func testAPI(f handlerFields) *API {
	c := &core{
		versionCache:   f.versionCache,
		queries:        f.queries,
		pool:           f.pool,
		tx:             f.tx,
		settings:       f.settings,
		keyring:        f.keyring,
		temporalClient: f.temporalClient,
		catalogCache:   f.catalogCache,
		connections:    f.connections,
		engines:        f.engines,
		kinds:          f.kinds,
		dialects:       f.dialects,
		audit:          f.audit,
	}
	c.history = history.New(c.queries)
	c.browse = browse.New(c.queries)
	c.spaces = spacesOver(c, c.queries)
	c.access = wsaccess.New(c.queries)
	h := &API{
		core:              c,
		OpsHandlers:       newOpsHandlers(c, f.schema),
		AgentHandlers:     newAgentHandlers(c, nil, nil, f.dialer),
		SemanticHandlers:  newSemanticHandlers(c),
		QueryHandlers:     newQueryHandlers(c, nil),
		CatalogHandlers:   newCatalogHandlers(c, f.binder),
		WorkspaceHandlers: newWorkspaceHandlers(c),
		AuthHandlers:      newAuthHandlers(c),
	}
	h.attentionCache = f.attentionCache
	return h
}

// directoryFunc and proberFunc adapt a test function to the auth seams.
type directoryFunc func(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error)

func (f directoryFunc) Authenticate(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error) {
	return f(ctx, cfg, username, password)
}

type proberFunc func(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult

func (f proberFunc) Probe(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult {
	return f(ctx, cfg, testUsername)
}

// scheduleFunc and engineStatusFunc adapt a test function to the status probes.
type scheduleFunc func(ctx context.Context, id string) (SystemStatusItemStatus, string)

func (f scheduleFunc) DescribeSchedule(ctx context.Context, id string) (SystemStatusItemStatus, string) {
	return f(ctx, id)
}

type engineStatusFunc func(ctx context.Context, e db.Engine) (SystemStatusItemStatus, string)

func (f engineStatusFunc) ProbeEngine(ctx context.Context, e db.Engine) (SystemStatusItemStatus, string) {
	return f(ctx, e)
}
