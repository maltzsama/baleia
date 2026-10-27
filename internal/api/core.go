package api

import (
	"context"
	"log/slog"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/content/history"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/storage"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/galileostudio/baleia/internal/workspaces/spaces"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// core is what every handler group shares (#365): Baleia's database, the
// connections and engines, the audit trail and the resolved settings. Each
// group embeds the same *core and owns the rest of its state.
type core struct {
	// logger is the process logger (#379); every group logs through it.
	logger  *slog.Logger
	queries db.Querier
	pool    *pgxpool.Pool
	// tx runs dependent writes in one transaction (poolTx outside tests).
	tx txRunner
	// settings is the resolved configuration the handlers read (#363).
	settings       Settings
	keyring        *crypto.Keyring
	temporalClient client.Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
	// versionCache: engine id@updated_at → version and health, read by the
	// engine listing, the status page and the attention items.
	versionCache *TTLStore[engineProbe]
	// s3 holds query results and exports (object storage).
	s3 *storage.S3Client
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
	// history reads content versions and the trash (#34), for every group
	// that shows a version.
	history *history.Service
	// browse reads the stored metadata of granted catalogs (#18); semantic
	// handlers use it to check a catalog is granted.
	browse *browse.Service
	// spaces stores spaces and their access lists (#131); every group that
	// writes content resolves its target space through it.
	spaces *spaces.Service
	// access answers whether a caller may use a workspace, its targets and
	// principals, and the /auth/me flags.
	access *wsaccess.Service
	// topology reads and edits how catalogs sit on engines.
	topology *topology.Service
}

// spacesOver builds the spaces service over store, creating a space and its
// first manager in one transaction of c.
func spacesOver(c *core, store spaces.Store) *spaces.Service {
	return spaces.New(store, func(ctx context.Context, fn func(spaces.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	})
}
