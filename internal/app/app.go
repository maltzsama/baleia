// Package app is the composition root the API and the worker share (#362): it
// builds the dependency graph both processes need, once, from the
// configuration. Each command adds only what is its own (the HTTP server, or
// the Temporal worker).
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/galileostudio/baleia/internal/agentauth"
	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/bootstrap"
	"github.com/galileostudio/baleia/internal/catalogidentity"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/glue"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/engines"
	"github.com/galileostudio/baleia/internal/identity/idptoken"
	"github.com/galileostudio/baleia/internal/identity/oidclogin"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/oracle"
	sqliteengine "github.com/galileostudio/baleia/internal/queryengine/sqlite"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/temporal/dial"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

// Options are what differs between the processes.
type Options struct {
	// Service names the process in metrics and traces ("api", "worker").
	Service string
	// PoolSize is the database pool size when BALEIA_DB_MAX_CONNS is unset.
	PoolSize int
	// Logger is the process logger (obs.NewLogger); nil discards.
	Logger *slog.Logger
}

// Core is the shared dependency graph. Close releases it.
type Core struct {
	Config *config.Config
	// Logger is injected into every component that logs (#379).
	Logger  *slog.Logger
	Metrics *metrics.Registry
	Pool    *pgxpool.Pool
	// Queries is the GORM-backed store, kept under the sqlc-shaped interface
	// the services still consume (stage A of #761, ADR 0020).
	Queries db.Querier
	// Store is the GORM connection every domain persists through (ADR 0020).
	Store *gorm.DB
	// StoreRepo is the GORM-backed store shared by the migrated domains.
	StoreRepo *repo.Repo
	// Keyring seals stored connection credentials; RegistryKeyring is nil
	// when registry encryption is disabled.
	Keyring         *crypto.Keyring
	RegistryKeyring *crypto.Keyring
	Temporal        client.Client
	// Dialer is the SSRF-safe dialer every outbound connection goes through
	// (docs/adr/0011).
	Dialer      *connections.Dialer
	Connections *connections.Registry
	// Dialects describe the SQL of every engine kind; built once and injected.
	Dialects *dialect.Registry
	// Engines run SQL (docs/adr/0002).
	Engines *queryengine.Manager
	Storage *storage.S3Client
	// TrinoAdmin is the catalog DDL identity (docs/adr/0005); Binder uses it.
	TrinoAdmin *trino.Client
	// OIDC runs OIDC sign-ins and refreshes identity-provider tokens;
	// IDPTokens keeps each user's (#648). The API and the worker share them.
	OIDC      *oidclogin.Service
	IDPTokens *idptoken.Tokens
	// TrinoHTTP is the HTTP client of the default cluster (TRINO_URL): the
	// dialer, the coordinator's CA bundle and Baleia's password login
	// (#646). The worker's Lens monitor uses it too.
	TrinoHTTP *http.Client
	// TrinoCA is the PEM bundle of BALEIA_TRINO_CA_FILE, empty when unset.
	TrinoCA string
	Binder  *bindtrino.Binder
	// Audit is best effort: a failed write is counted, never fatal.
	Audit *audit.Postgres

	closers []func()
}

// New builds the graph. On error everything built so far is released.
func New(ctx context.Context, cfg *config.Config, opts Options) (_ *Core, err error) {
	if opts.Service == "" {
		return nil, ErrNoService
	}
	c := &Core{Config: cfg, Logger: cmp.Or(opts.Logger, slog.New(slog.DiscardHandler))}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	shutdownTracing, err := obs.Setup(ctx, c.Logger, cfg.TracingEnabled, opts.Service)
	if err != nil {
		return nil, fmt.Errorf("tracing setup: %w", err)
	}
	// Tracing flushes after everything else is closed. The process context is
	// cancelled by then, so the flush gets its own bounded one.
	c.onClose(func() {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(fctx)
	})

	c.Metrics = metrics.New(opts.Service)
	// The pgx pool and the GORM store share one per-process connection
	// budget: the store gets a quarter and the pgx pool the rest.
	totalConns := int(cfg.PoolSize(opts.PoolSize))
	storeConns := totalConns / 4
	if storeConns < 1 {
		storeConns = 1
	}
	poolConns := totalConns - storeConns
	if poolConns < 1 {
		poolConns = 1
		storeConns = totalConns - 1
	}
	var minConns int32
	if cfg.DBMinConns > 0 && cfg.DBMinConns <= math.MaxInt32 {
		minConns = int32(cfg.DBMinConns)
	}
	if int(minConns) > poolConns {
		minConns = int32(poolConns)
	}
	c.Pool, err = dbpool.Open(ctx, cfg.DatabaseURL, dbpool.Options{
		MaxConns: int32(poolConns), MinConns: minConns, MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime, HealthCheckPeriod: cfg.DBHealthCheckPeriod,
		StatementTimeout: cfg.DBStatementTimeout, IdleInTxTimeout: cfg.DBIdleInTxTimeout, LockTimeout: cfg.DBLockTimeout,
		Tracer: obs.PGXTracer(c.Metrics.NewPoolTracer()),
	})
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	c.onClose(c.Pool.Close)
	c.Metrics.WatchPool(c.Pool)

	backend, err := store.ResolveBackend(cfg.StoreBackend, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("store backend: %w", err)
	}
	c.Store, err = store.Open(ctx, cfg.DatabaseURL, backend, store.Options{
		MaxOpenConns:     storeConns,
		Logger:           c.Logger,
		StatementTimeout: cfg.DBStatementTimeout,
		IdleInTxTimeout:  cfg.DBIdleInTxTimeout,
		LockTimeout:      cfg.DBLockTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("gorm store: %w", err)
	}
	c.onClose(func() { _ = store.Close(c.Store) })
	c.StoreRepo = repo.New(c.Store)
	c.Queries = c.StoreRepo

	// Credentials are sealed with BALEIA_ENCRYPTION_KEY (never JWT_SECRET).
	// With stored credentials and no key, refuse to start rather than serve
	// rows that cannot be read.
	if c.Keyring, err = bootstrap.LoadKeyring(ctx, c.Logger, cfg, c.Queries.CountEncryptedConnections); err != nil {
		return nil, fmt.Errorf("credential keyring: %w", err)
	}
	if c.RegistryKeyring, err = bootstrap.LoadRegistryKeyring(cfg); err != nil {
		return nil, fmt.Errorf("registry keyring: %w", err)
	}

	temporalMetrics, flushTemporal := c.Metrics.TemporalHandler()
	c.onClose(func() { _ = flushTemporal() })
	if c.Temporal, err = dial.Temporal(ctx, c.Logger, cfg.TemporalHost, cfg.TemporalNamespace, temporalMetrics, 30*time.Second, obs.ClientInterceptors()); err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}
	c.onClose(c.Temporal.Close)

	c.Dialer, err = connections.NewDialer(connections.DialerOptions{
		DenyCIDRs:  cfg.ConnectionsDenyCIDRs,
		AllowCIDRs: cfg.ConnectionsAllowCIDRs,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid connection network settings: %w", err)
	}
	c.Dialects = dialect.NewRegistry()
	pOpts := providers.Options{
		Dialects: c.Dialects,
		Secrets:  connections.SecretResolver{FileBaseDir: cfg.SecretFileBaseDir},
		SQLite:   sqlite.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite, Logger: c.Logger},
		Glue:     glue.Options{AllowAmbientCredentials: cfg.AllowAmbientAWSCredentials, AllowInsecureEndpoint: cfg.GlueAllowInsecureEndpoint},
	}
	c.Connections, err = providers.New(c.Dialer, pOpts)
	if err != nil {
		return nil, fmt.Errorf("connection providers: %w", err)
	}
	// The SQLite engine confines files to the same root the provider uses
	// (docs/adr/0011).
	kinds, err := engines.NewRegistry(engines.Options{
		Dialects: c.Dialects,
		SQLite:   sqliteengine.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite},
		Oracle:   oracle.Options{LOBMaxBytes: cfg.OracleLOBMaxBytes},
		Logger:   c.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("engine kinds: %w", err)
	}
	c.Engines = queryengine.NewManager(c.Queries, c.Keyring, c.Connections, c.Dialer, kinds)
	// Catalog managers set to per-user identity authorize each user with
	// their identity-provider token, carried by every query session (#648).
	oidcStore := oidclogin.Store(c.StoreRepo)
	tokenStore := idptoken.Store(c.StoreRepo)
	tokenTx := c.StoreRepo.TxIDPToken
	c.OIDC = oidclogin.New(oidcStore, c.Queries, c.Keyring, oidclogin.DiscoverIssuer, oidclogin.Config{
		PublicURL: cfg.PublicURL,
		CacheTTL:  time.Hour,
		Logger:    c.Logger,
	})
	c.IDPTokens = idptoken.New(tokenStore, tokenTx, c.Keyring, c.OIDC, c.Logger)
	c.OIDC.SetTokens(c.IDPTokens)
	c.Engines.SetCatalogCredentials(catalogidentity.New(c.Queries, c.Keyring, c.Connections, c.IDPTokens, c.Logger))

	c.Storage, err = storage.New(storage.Options{
		Endpoint:  cfg.S3Endpoint,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Bucket:    cfg.S3Bucket,
		Region:    cfg.S3Region,
		UseSSL:    cfg.S3UseSSL,
	})
	if err != nil {
		return nil, err
	}
	c.Storage.SetMetrics(c.Metrics.Storage())

	if cfg.TrinoCAFile != "" {
		ca, err := os.ReadFile(cfg.TrinoCAFile)
		if err != nil {
			return nil, fmt.Errorf("BALEIA_TRINO_CA_FILE: %w", err)
		}
		c.TrinoCA = string(ca)
	}
	hc, err := trino.Authenticated(c.Dialer.HTTPClient(60*time.Second), cfg.TrinoAdminUser, cfg.TrinoPassword, c.TrinoCA)
	if err != nil {
		return nil, fmt.Errorf("default trino client: %w", err)
	}
	c.TrinoHTTP = obs.WrapHTTPClient(hc)
	c.TrinoAdmin = trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoAdminUser, c.TrinoHTTP)
	c.Binder = bindtrino.New(c.Logger, c.TrinoAdmin, c.Queries).WithRegistryKeyring(c.RegistryKeyring).
		WithIcebergMinRetention(cfg.MaintenanceMinRetention())
	c.Audit = audit.NewPostgres(c.Logger, c.StoreRepo, c.Connections.SecretKeys(), c.Metrics.Audit().WriteFailed)
	return c, nil
}

// onClose registers f to run on Close, in reverse order of registration.
func (c *Core) onClose(f func()) { c.closers = append(c.closers, f) }

// AgentTokens is the GORM-backed agent-token store, or sqlc in tests without
// one.
func (c *Core) AgentTokens() agentauth.Store {
	if c.StoreRepo != nil {
		return c.StoreRepo
	}
	return c.Queries
}

// Close releases the graph, last built first. It is safe to call twice.
func (c *Core) Close() {
	for i := len(c.closers) - 1; i >= 0; i-- {
		c.closers[i]()
	}
	c.closers = nil
}

// ErrNoService is returned by New when Options.Service is empty.
var ErrNoService = errors.New("app: Options.Service is required")
