package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/wsticket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

var ErrWorkspaceNotFound = errors.New("workspace not found")

type Handlers struct {
	queries        db.Querier
	pool           *pgxpool.Pool
	cfg            *config.Config
	keyring        *crypto.Keyring
	temporalClient client.Client
	s3             *storage.S3Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
	versionCache   *TTLStore[engineProbe] // engine id@updated_at → version and health
	// connections holds every connection provider (docs/adr/0002).
	connections *connections.Registry
	// engines runs SQL (docs/adr/0002); binder binds catalogs into Trino as
	// the admin identity (docs/adr/0005, #80). Both are built at the wiring site.
	engines engineSource
	binder  *bindtrino.Binder
	metrics apiMetrics
	// audit records security and data operations (#45; call sites #43).
	audit audit.Recorder
	// dialer is the SSRF-safe dialer for transient engines (#12).
	dialer *connections.Dialer
	// content is the only writer of saved content and its versions (#34).
	content *content.Service
	// wsTickets issues the single-use WebSocket tickets (#26); the WS handler
	// shares this store.
	wsTickets *wsticket.Store
	// dummyHash is compared against on the unknown-account and SSO-only paths
	// so timing does not reveal whether an account exists (#51).
	dummyHashOnce sync.Once
	dummyHash     string
}

// SetWSTickets wires the shared WebSocket ticket store (#26).
func (h *Handlers) SetWSTickets(store *wsticket.Store) { h.wsTickets = store }

func (h *Handlers) wsTicketStore() *wsticket.Store {
	if h.wsTickets == nil {
		h.wsTickets = wsticket.New()
	}
	return h.wsTickets
}

// contentService returns the content writer, constructing one for handlers
// built by tests without NewHandlers.
func (h *Handlers) contentService() *content.Service {
	if h.content != nil {
		return h.content
	}
	return content.New(h.pool, h.queries)
}

// apiMetrics are the API's producers (#87); all nil-safe, zero without
// SetMetrics.
type apiMetrics struct {
	auth    *metrics.Auth
	authz   *metrics.Authz
	catalog *metrics.Catalog
}

// AuthMetrics is the authentication producer (for the server's rate limiter).
func (h *Handlers) AuthMetrics() *metrics.Auth { return h.metrics.auth }

// SetMetrics registers the API's producers on reg and exports bindings by
// lifecycle state.
func (h *Handlers) SetMetrics(reg *metrics.Registry) {
	h.metrics = apiMetrics{auth: reg.Auth(), authz: reg.Authz(), catalog: reg.Catalog()}
	reg.WatchBindingStates(func(ctx context.Context) (map[string]int64, error) {
		rows, err := h.queries.CountManagedBindingsByState(ctx)
		if err != nil {
			return nil, err
		}
		out := make(map[string]int64, len(rows))
		for _, r := range rows {
			out[r.LifecycleState] = r.Count
		}
		return out, nil
	})
}

// engineSource returns engines; *queryengine.Manager implements it.
type engineSource interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
	DefaultTrino(ctx context.Context) (queryengine.Engine, error)
}

func NewHandlers(queries db.Querier, pool *pgxpool.Pool, cfg *config.Config, keyring *crypto.Keyring, temporalClient client.Client, s3 *storage.S3Client, conns *connections.Registry, engines engineSource, binder *bindtrino.Binder) *Handlers {
	return &Handlers{
		connections:    conns,
		queries:        queries,
		pool:           pool,
		cfg:            cfg,
		keyring:        keyring,
		temporalClient: temporalClient,
		s3:             s3,
		engines:        engines,
		binder:         binder,
		catalogCache:   NewTTLStore[[]workspaceCatalogEntry](60 * time.Second),
		versionCache:   NewTTLStore[engineProbe](engineVersionTTL),
		audit:          audit.Nop{},
		content:        content.New(pool, queries),
		wsTickets:      wsticket.New(),
	}
}

// SetDialer wires the dialer connection tests build transient engines with.
func (h *Handlers) SetDialer(d *connections.Dialer) { h.dialer = d }

// SetAudit wires the audit recorder (#45).
func (h *Handlers) SetAudit(r audit.Recorder) { h.audit = r }

// ── helpers ───────────────────────────────────────────────────────────────────

func dbUserToPublic(u db.User) UserPublic {
	id := openapi_types.UUID(u.ID.Bytes)
	email := openapi_types.Email(u.Email)
	return UserPublic{
		Id:           id,
		Name:         u.Name,
		Email:        email,
		Role:         UserPublicRole(u.Role),
		AuthProvider: UserPublicAuthProvider(u.AuthProvider),
		CreatedAt:    pgTimestampToTime(u.CreatedAt),
	}
}

func pgTimestampToTime(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

func uuidFromString(s string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	err := uid.Scan(s)
	return uid, err
}

func workspaceIDFromRequest(id openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func requireWorkspaceAccess(ctx context.Context, queries db.Querier, workspaceID openapi_types.UUID) error {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return err
	}

	hasAccess, err := queries.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{
		ID:     workspaceIDFromRequest(workspaceID),
		UserID: uid,
	})
	if err != nil {
		return err
	}
	if !hasAccess {
		return ErrWorkspaceNotFound
	}
	return nil
}

func errUnauthorized(msg string) Error {
	return Error{Code: "unauthorized", Message: msg}
}

func errForbidden(msg string) Error {
	return Error{Code: "forbidden", Message: msg}
}

func errNotFound(msg string) Error {
	return Error{Code: "not_found", Message: msg}
}

func errConflict(msg string) Error {
	return Error{Code: "conflict", Message: msg}
}

// respondError is only used by the manual (OIDC) routes.
func respondError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}
