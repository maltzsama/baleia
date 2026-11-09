package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/content/history"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/identity/idptoken"
	"github.com/galileostudio/baleia/internal/identity/oidclogin"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/monitoring/events"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/store/repo"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/galileostudio/baleia/internal/workspaces/spaces"
	"github.com/galileostudio/baleia/internal/wsticket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

var ErrWorkspaceNotFound = errors.New("workspace not found")

type API struct {
	// core is embedded at the top so the shared fields and helpers resolve
	// here, not ambiguously through every group.
	*core
	*AuthHandlers
	*WorkspaceHandlers
	*CatalogHandlers
	*QueryHandlers
	*SemanticHandlers
	*AgentHandlers
	*OpsHandlers
}

// The handlers implement the whole generated strict server; the split by
// domain (#365) keeps it composed into one value.
var _ StrictServerInterface = (*API)(nil)

// Drain waits, until ctx ends, for the work the handlers left running in the
// background: the password-reset deliveries (#422). The server calls it
// after the HTTP server stopped taking requests.
func (a *API) Drain(ctx context.Context) error {
	return a.resets.Drain(ctx)
}

// MailSender is the transactional e-mail surface (#67).
type MailSender interface {
	Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error
}

// mailSender uses the injected mailer, or SMTP from the configuration.
func (h *AuthHandlers) mailSender() MailSender {
	if h.mailer != nil {
		return h.mailer
	}
	return mail.New(mail.Config{
		Host:          h.settings.Mail.SMTPHost,
		Port:          h.settings.Mail.SMTPPort,
		User:          h.settings.Mail.SMTPUser,
		Password:      h.settings.Mail.SMTPPassword,
		From:          h.settings.Mail.SMTPFrom,
		DefaultLocale: h.settings.Mail.DefaultLocale,
		Logger:        h.logger,
	})
}

func (h *QueryHandlers) wsTicketStore() *wsticket.Store {
	if h.wsTickets == nil {
		h.wsTickets = wsticket.New()
	}
	return h.wsTickets
}

// contentService returns the content writer, constructing one for handlers
// built by tests without NewAPI.
func (h *core) contentService() *content.Service {
	if h.content != nil {
		return h.content
	}
	if h.store != nil {
		r := repo.New(h.store)
		return content.New(r, r.TxContent)
	}
	tx := func(ctx context.Context, fn func(content.Store) error) error {
		return h.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	return content.New(h.queries, tx)
}

// apiMetrics are the API's producers (#87); all nil-safe, zero without a
// metrics registry.
type apiMetrics struct {
	auth      *metrics.Auth
	authz     *metrics.Authz
	catalog   *metrics.Catalog
	directory *metrics.Directory
}

// AuthMetrics is the authentication producer (for the server's rate limiter).
func (h *API) AuthMetrics() *metrics.Auth { return h.metrics.auth }

// registerMetrics registers the API's producers on reg and exports bindings
// by lifecycle state.
func (h *core) registerMetrics(reg *metrics.Registry) {
	h.metrics = apiMetrics{auth: reg.Auth(), authz: reg.Authz(), catalog: reg.Catalog(), directory: reg.Directory()}
	reg.WatchBindingStates(h.logger, func(ctx context.Context) (map[string]int64, error) {
		rows, err := h.topology.BindingStates(ctx)
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

// engineKinds says which engine kinds have an adapter and builds one-off
// engines for connection tests (#364); the engine manager implements it.
type engineKinds interface {
	HasKind(kind string) bool
	Transient(ctx context.Context, kind string, cfg connections.Config) (queryengine.Engine, error)
}

// Deps is everything the API handlers need, built by the composition root
// (internal/app) and grouped by area (#363).
type Deps struct {
	Data          DataDeps
	Engines       EngineDeps
	Observability ObservabilityDeps
	Settings      Settings
	// Keyring seals stored credentials; nil while none is stored.
	Keyring  *crypto.Keyring
	Temporal client.Client
	Storage  *storage.S3Client
	// Assistant receives the turn runner once the server has built it; nil
	// when the assistant is not provisioned.
	Assistant *assistant.RunnerSlot
	// Identity shares the OIDC sign-in service and the identity-provider
	// tokens with the worker (#648); nil builds a service of its own.
	Identity IdentityDeps
	// WSTickets issues the single-use WebSocket tickets (#26); the WebSocket
	// handler consumes from the same store. Nil builds one.
	WSTickets *wsticket.Store
	// Monitoring fans monitoring changes out to the SSE endpoint (#733).
	Monitoring *events.Hub
}

// ObservabilityDeps are optional: a nil Audit records nothing, a nil
// Metrics registers nothing and a nil Logger discards.
type ObservabilityDeps struct {
	Audit   audit.Recorder
	Metrics *metrics.Registry
	Logger  *slog.Logger
}

// IdentityDeps are the OIDC sign-in service and the store of users'
// identity-provider tokens (#648).
type IdentityDeps struct {
	OIDC      *oidclogin.Service
	IDPTokens *idptoken.Tokens
}

// DataDeps is Baleia's own database.
type DataDeps struct {
	// Queries is accepted for source compatibility; NewAPI builds every
	// service over Store and ignores it.
	Queries db.Querier
	Pool    *pgxpool.Pool
	// Store is the GORM connection every service persists through (ADR 0020).
	Store *gorm.DB
}

// EngineDeps are the connections, engines and catalog bindings.
type EngineDeps struct {
	Connections *connections.Registry
	Engines     engineSource
	Kinds       engineKinds
	Dialects    *dialect.Registry
	Binder      *bindtrino.Binder
	// Dialer is the SSRF-safe dialer outbound calls go through (#12, #378).
	Dialer *connections.Dialer
}

// NewAPI builds the handlers. It fails when a required dependency is
// missing instead of leaving a nil to be found at request time.
func NewAPI(d Deps) (*API, error) {
	var missing []string
	for name, absent := range map[string]bool{
		"Data.Store":          d.Data.Store == nil,
		"Data.Pool":           d.Data.Pool == nil,
		"Temporal":            d.Temporal == nil,
		"Storage":             d.Storage == nil,
		"Engines.Connections": d.Engines.Connections == nil,
		"Engines.Engines":     d.Engines.Engines == nil,
		"Engines.Kinds":       d.Engines.Kinds == nil,
		"Engines.Dialects":    d.Engines.Dialects == nil,
		"Engines.Binder":      d.Engines.Binder == nil,
		"Engines.Dialer":      d.Engines.Dialer == nil,
	} {
		if absent {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("api: missing dependencies: %s", strings.Join(missing, ", "))
	}
	c := &core{
		logger:         cmp.Or(d.Observability.Logger, slog.New(slog.DiscardHandler)),
		versionCache:   NewTTLStore[engineProbe](engineVersionTTL),
		s3:             d.Storage,
		queries:        d.Data.Queries,
		store:          d.Data.Store,
		pool:           d.Data.Pool,
		tx:             storeTx{db: d.Data.Store},
		settings:       d.Settings,
		keyring:        d.Keyring,
		temporalClient: d.Temporal,
		catalogCache:   NewTTLStore[[]workspaceCatalogEntry](60 * time.Second),
		functionsCache: NewTTLStore[[]metadata.FunctionReference](time.Hour),
		connections:    d.Engines.Connections,
		engines:        d.Engines.Engines,
		kinds:          d.Engines.Kinds,
		dialects:       d.Engines.Dialects,
		audit:          d.Observability.Audit,
		oidcLogin:      d.Identity.OIDC,
		idpTokens:      d.Identity.IDPTokens,
		monitoringHub:  d.Monitoring,
	}
	if c.audit == nil {
		c.audit = audit.Nop{}
	}
	// Every service persists through the GORM store (ADR 0020); the
	// sqlc-shaped interface is only the narrow surface they consume.
	storeRepo := repo.New(c.store)
	c.queries = storeRepo
	c.content = content.New(storeRepo, storeRepo.TxContent)
	c.history = history.New(storeRepo)
	c.browse = browse.New(storeRepo)
	c.spaces = spaces.New(storeRepo, storeRepo.TxSpaces)
	c.access = wsaccess.New(storeRepo)
	c.topology = topology.New(storeRepo)
	assistantService := assistant.New(storeRepo, d.Keyring)
	h := &API{
		core:              c,
		AuthHandlers:      newAuthHandlers(c),
		WorkspaceHandlers: newWorkspaceHandlers(c),
		CatalogHandlers:   newCatalogHandlers(c, d.Engines.Binder),
		QueryHandlers:     newQueryHandlers(c, d.WSTickets),
		SemanticHandlers:  newSemanticHandlers(c),
		AgentHandlers:     newAgentHandlers(c, assistantService, d.Assistant, d.Engines.Dialer),
		OpsHandlers:       newOpsHandlers(c, poolSchema{pool: d.Data.Pool}),
	}
	if d.Observability.Metrics != nil {
		h.registerMetrics(d.Observability.Metrics)
	}
	return h, nil
}

// AssistantService exposes the persistence service to the wiring site.
func (h *AgentHandlers) AssistantService() *assistant.Service { return h.assistant }

// ── helpers ───────────────────────────────────────────────────────────────────

func dbUserToPublic(u db.User) UserPublic {
	id := openapi_types.UUID(u.ID.Bytes)
	email := openapi_types.Email(u.Email)
	out := UserPublic{
		Id:           id,
		Name:         u.Name,
		Email:        email,
		Role:         UserPublicRole(u.Role),
		AuthProvider: UserPublicAuthProvider(u.AuthProvider),
		Status:       UserPublicStatus(u.Status),
		CreatedAt:    pgTimestampToTime(u.CreatedAt),
	}
	if u.Kind != "" {
		kind := UserPublicKind(u.Kind)
		out.Kind = &kind
	}
	if u.Locale != nil {
		locale := Locale(*u.Locale)
		out.Locale = &locale
	}
	return out
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

func requireWorkspaceAccess(ctx context.Context, svc *wsaccess.Service, workspaceID openapi_types.UUID) error {
	uid, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return err
	}
	actor := wsaccess.Actor{User: uid}
	if auth.ActorKindFromContext(ctx) == auth.ActorAgent {
		agentID, err := uuidFromString(auth.AgentIDFromContext(ctx))
		if err != nil {
			return ErrWorkspaceNotFound
		}
		actor.Agent = agentID
	}
	err = svc.Check(ctx, workspaceIDFromRequest(workspaceID), actor)
	if errors.Is(err, wsaccess.ErrNotFound) {
		return ErrWorkspaceNotFound
	}
	return err
}

// requireWorkspaceRole requires at least minRole in the workspace (#653). A
// non-member is ErrForbidden here too: membership and authentication are
// already enforced by requireWorkspaceAccess.
func (h *core) requireWorkspaceRole(ctx context.Context, workspaceID openapi_types.UUID, minRole authz.WorkspaceRole) error {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return authz.ErrForbidden
	}
	_, err = h.authz().RequireWorkspaceRole(ctx, p, workspaceIDFromRequest(workspaceID), minRole)
	if errors.Is(err, authz.ErrNotFound) {
		return authz.ErrForbidden
	}
	return err
}
