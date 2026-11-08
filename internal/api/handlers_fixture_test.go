package api

import (
	"context"
	"log/slog"
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/content/history"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/store/repo"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.temporal.io/sdk/client"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// handlerFields are the dependencies a test sets. Tests build handlers only
// through testAPI, so splitting the handlers by domain (#365) changes
// this file, not every fixture.
type handlerFields struct {
	queries        db.Querier
	store          *gorm.DB
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
		logger:         discardLogger,
		versionCache:   f.versionCache,
		queries:        f.queries,
		store:          f.store,
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
	if c.store != nil {
		contentStore := repo.New(c.store)
		c.content = content.New(contentStore, contentStore.TxContent)
		c.history = history.New(contentStore)
	}
	c.browse = browse.New(c.queries)
	c.spaces = spacesOver(c, c.queries)
	c.access = wsaccess.New(c.queries)
	c.topology = topology.New(c.queries)
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

// openTestStore opens GORM over a test pool, as the application does when the
// GORM store is configured. Handler tests pass it through handlerFields.store
// to exercise the migrated path (#769).
func openTestStore(t *testing.T, pool *pgxpool.Pool) *gorm.DB {
	t.Helper()
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
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

// discardLogger is the logger of tests that do not read the log.
var discardLogger = slog.New(slog.DiscardHandler)
