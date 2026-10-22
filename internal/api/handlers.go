package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	assistantdb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/galileostudio/baleia/internal/dialect"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/mail"
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
	kinds   engineKinds
	// dialects describe each engine kind's SQL (built once, injected).
	dialects *dialect.Registry
	binder   *bindtrino.Binder
	metrics  apiMetrics
	// audit records security and data operations (#45; call sites #43).
	audit audit.Recorder
	// dialer is the SSRF-safe dialer for transient engines (#12).
	dialer *connections.Dialer
	// content is the only writer of saved content and its versions (#34).
	content *content.Service
	// assistant owns assistant settings, agents and conversations (#62).
	assistant *assistant.Service
	// assistantRunner runs the MCP-backed turns and owns the SSE hub.
	assistantRunner *assistant.Runner
	// wsTickets issues the single-use WebSocket tickets (#26); the WS handler
	// shares this store.
	wsTickets *wsticket.Store
	// systemStatusSchedule and systemStatusEngine are the status page's probe
	// seams (#291). Tests inject them without a Temporal server or an engine.
	systemStatusSchedule func(ctx context.Context, id string) (SystemStatusItemStatus, string)
	systemStatusEngine   func(ctx context.Context, e db.Engine) (SystemStatusItemStatus, string)
	// dummyHash is compared against on the unknown-account and SSO-only paths
	// so timing does not reveal whether an account exists (#51).
	dummyHashOnce sync.Once
	dummyHash     string
	// loginDelay slows repeated login failures per account (#51).
	loginDelayStore *LoginDelay
	loginDelayOnce  sync.Once
	// mailer sends password-reset and invite e-mails (#51) localized by the
	// recipient's preference (#67). Tests inject a fake so the paths run
	// without an SMTP server.
	mailer MailSender
	// oidcCache holds discovered providers keyed by id + config version (#280).
	oidcCache sync.Map
	// attentionCache caches the computed attention items for 30 s per user (#261).
	attentionCache *TTLStore[[]AttentionItem]
	attentionOnce  sync.Once
	// ldapAuthenticate verifies directory credentials (#255). Tests inject a
	// fake so the handler branch runs without a directory server.
	ldapAuthenticate func(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error)
	// idpProbe probes a provider configuration without persisting anything
	// (#286). Tests inject a fake so the handler runs without a network.
	idpProbe func(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult
}

// MailSender is the transactional e-mail surface (#67).
type MailSender interface {
	Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error
}

// SetMailer injects the password-reset/invite mailer (tests).
func (h *Handlers) SetMailer(m MailSender) { h.mailer = m }

// mailSender uses the injected mailer, or SMTP from the configuration.
func (h *Handlers) mailSender() MailSender {
	if h.mailer != nil {
		return h.mailer
	}
	return mail.New(mail.Config{
		Host:          h.cfg.SMTPHost,
		Port:          h.cfg.SMTPPort,
		User:          h.cfg.SMTPUser,
		Password:      h.cfg.SMTPPassword,
		From:          h.cfg.SMTPFrom,
		DefaultLocale: h.cfg.DefaultLocale,
	})
}


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
	auth      *metrics.Auth
	authz     *metrics.Authz
	catalog   *metrics.Catalog
	directory *metrics.Directory
}

// AuthMetrics is the authentication producer (for the server's rate limiter).
func (h *Handlers) AuthMetrics() *metrics.Auth { return h.metrics.auth }

// registerMetrics registers the API's producers on reg and exports bindings
// by lifecycle state.
func (h *Handlers) registerMetrics(reg *metrics.Registry) {
	h.metrics = apiMetrics{auth: reg.Auth(), authz: reg.Authz(), catalog: reg.Catalog(), directory: reg.Directory()}
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
	Config        *config.Config
	// Keyring seals stored credentials; nil while none is stored.
	Keyring  *crypto.Keyring
	Temporal client.Client
	Storage  *storage.S3Client
	// WSTickets issues the single-use WebSocket tickets (#26); the WebSocket
	// handler consumes from the same store. Nil builds one.
	WSTickets *wsticket.Store
}

// ObservabilityDeps are optional: a nil Audit records nothing and a nil
// Metrics registers nothing.
type ObservabilityDeps struct {
	Audit   audit.Recorder
	Metrics *metrics.Registry
}

// DataDeps is Baleia's own database.
type DataDeps struct {
	Queries db.Querier
	Pool    *pgxpool.Pool
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

// NewHandlers builds the handlers. It fails when a required dependency is
// missing instead of leaving a nil to be found at request time.
func NewHandlers(d Deps) (*Handlers, error) {
	var missing []string
	for name, absent := range map[string]bool{
		"Data.Queries":        d.Data.Queries == nil,
		"Data.Pool":           d.Data.Pool == nil,
		"Config":              d.Config == nil,
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
	h := &Handlers{
		connections:    d.Engines.Connections,
		queries:        d.Data.Queries,
		pool:           d.Data.Pool,
		cfg:            d.Config,
		keyring:        d.Keyring,
		temporalClient: d.Temporal,
		s3:             d.Storage,
		engines:        d.Engines.Engines,
		kinds:          d.Engines.Kinds,
		dialects:       d.Engines.Dialects,
		binder:         d.Engines.Binder,
		dialer:         d.Engines.Dialer,
		catalogCache:   NewTTLStore[[]workspaceCatalogEntry](60 * time.Second),
		versionCache:   NewTTLStore[engineProbe](engineVersionTTL),
		audit:          d.Observability.Audit,
		content:        content.New(d.Data.Pool, d.Data.Queries),
		assistant:      assistant.New(assistantdb.New(d.Data.Pool), d.Keyring),
		wsTickets:      d.WSTickets,
	}
	if h.audit == nil {
		h.audit = audit.Nop{}
	}
	if h.wsTickets == nil {
		h.wsTickets = wsticket.New()
	}
	if d.Observability.Metrics != nil {
		h.registerMetrics(d.Observability.Metrics)
	}
	return h, nil
}



// SetAssistantRunner wires the turn engine and its SSE hub (#62).
func (h *Handlers) SetAssistantRunner(r *assistant.Runner) { h.assistantRunner = r }

// AssistantService exposes the persistence service to the wiring site.
func (h *Handlers) AssistantService() *assistant.Service { return h.assistant }

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
	// An agent acts only in the workspaces it is bound to, even though its owner
	// remains a member of them (#59).
	if auth.ActorKindFromContext(ctx) == auth.ActorAgent {
		agentID, err := uuidFromString(auth.AgentIDFromContext(ctx))
		if err != nil {
			return ErrWorkspaceNotFound
		}
		bound, err := queries.AgentWorkspaceBound(ctx, db.AgentWorkspaceBoundParams{
			AgentID:     agentID,
			WorkspaceID: workspaceIDFromRequest(workspaceID),
		})
		if err != nil {
			return err
		}
		if !bound {
			return ErrWorkspaceNotFound
		}
	}
	return nil
}
