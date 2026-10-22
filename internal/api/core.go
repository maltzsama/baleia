package api

import (
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// core is what every handler group shares (#365): Baleia's database, the
// connections and engines, the audit trail and the resolved settings. Each
// group embeds the same *core and owns the rest of its state.
type core struct {
	queries db.Querier
	pool    *pgxpool.Pool
	// tx runs dependent writes in one transaction (poolTx outside tests).
	tx txRunner
	// settings is the resolved configuration the handlers read (#363).
	settings       Settings
	keyring        *crypto.Keyring
	temporalClient client.Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
	// connections holds every connection provider (docs/adr/0002).
	connections *connections.Registry
	// engines runs SQL (docs/adr/0002), built at the wiring site.
	engines engineSource
	kinds   engineKinds
	// dialects describe each engine kind's SQL (built once, injected).
	dialects *dialect.Registry
	metrics  apiMetrics
	// audit records security and data operations (#45; call sites #43).
	audit audit.Recorder
	// content is the only writer of saved content and its versions (#34).
	content *content.Service
}
