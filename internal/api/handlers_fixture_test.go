package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
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
	return &API{
		core: c,
		OpsHandlers: &OpsHandlers{
			core:           c,
			schema:         f.schema,
			attentionCache: f.attentionCache,
		},
		AgentHandlers: &AgentHandlers{
			core:   c,
			dialer: f.dialer,
		},
		SemanticHandlers: &SemanticHandlers{
			core: c,
		},
		QueryHandlers: &QueryHandlers{
			core: c,
		},
		CatalogHandlers: &CatalogHandlers{
			core:   c,
			binder: f.binder,
		},
		WorkspaceHandlers: &WorkspaceHandlers{
			core: c,
		},
		AuthHandlers: newAuthHandlers(c),
	}
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
