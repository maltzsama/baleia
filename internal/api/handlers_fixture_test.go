package api

import (
	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// handlerFields are the dependencies a test sets. Tests build handlers only
// through testHandlers, so splitting the handlers by domain (#365) changes
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

// testHandlers builds handlers from the fields a test sets; the rest stay zero.
func testHandlers(f handlerFields) *Handlers {
	c := &core{
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
	return &Handlers{
		core: c,
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
			core:         c,
			versionCache: f.versionCache,
			binder:       f.binder,
		},
		WorkspaceHandlers: &WorkspaceHandlers{
			core: c,
		},
		AuthHandlers:   &AuthHandlers{core: c},
		schema:         f.schema,
		attentionCache: f.attentionCache,
	}
}
