// Package app is the composition root the API and the worker share (#362): it
// builds the dependency graph both processes need, once, from the
// configuration. Each command adds only what is its own (the HTTP server, or
// the Temporal worker).
package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/bootstrap"
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
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/oracle"
	sqliteengine "github.com/galileostudio/baleia/internal/queryengine/sqlite"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/temporal/dial"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// Options are what differs between the processes.
type Options struct {
	// Service names the process in metrics and traces ("api", "worker").
	Service string
	// PoolSize is the database pool size when BALEIA_DB_MAX_CONNS is unset.
	PoolSize int
}

// Core is the shared dependency graph. Close releases it.
type Core struct {
	Config  *config.Config
	Metrics *metrics.Registry
	Pool    *pgxpool.Pool
	Queries *db.Queries
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
	Binder     *bindtrino.Binder
	// Audit is best effort: a failed write is counted, never fatal.
	Audit *audit.Postgres

	closers []func()
}

// New builds the graph. On error everything built so far is released.
func New(ctx context.Context, cfg *config.Config, opts Options) (_ *Core, err error) {
	if opts.Service == "" {
		return nil, ErrNoService
	}
	c := &Core{Config: cfg}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	shutdownTracing, err := obs.Setup(ctx, cfg.TracingEnabled, opts.Service)
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
	var minConns int32
	if cfg.DBMinConns > 0 && cfg.DBMinConns <= math.MaxInt32 {
		minConns = int32(cfg.DBMinConns)
	}
	c.Pool, err = dbpool.Open(ctx, cfg.DatabaseURL, dbpool.Options{
		MaxConns: cfg.PoolSize(opts.PoolSize), MinConns: minConns, MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime, HealthCheckPeriod: cfg.DBHealthCheckPeriod,
		StatementTimeout: cfg.DBStatementTimeout, IdleInTxTimeout: cfg.DBIdleInTxTimeout, LockTimeout: cfg.DBLockTimeout,
		Tracer: obs.PGXTracer(c.Metrics.NewPoolTracer()),
	})
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	c.onClose(c.Pool.Close)
	c.Metrics.WatchPool(c.Pool)
	c.Queries = db.New(c.Pool)

	// Credentials are sealed with BALEIA_ENCRYPTION_KEY (never JWT_SECRET).
	// With stored credentials and no key, refuse to start rather than serve
	// rows that cannot be read.
	if c.Keyring, err = bootstrap.LoadKeyring(ctx, cfg, c.Queries.CountEncryptedConnections); err != nil {
		return nil, fmt.Errorf("credential keyring: %w", err)
	}
	if c.RegistryKeyring, err = bootstrap.LoadRegistryKeyring(cfg); err != nil {
		return nil, fmt.Errorf("registry keyring: %w", err)
	}

	temporalMetrics, flushTemporal := c.Metrics.TemporalHandler()
	c.onClose(func() { _ = flushTemporal() })
	if c.Temporal, err = dial.Temporal(ctx, cfg.TemporalHost, temporalMetrics, 30*time.Second, obs.ClientInterceptors()); err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}
	c.onClose(c.Temporal.Close)

	c.Dialer, err = connections.NewDialer(connections.DialerOptions{
		DenyCIDRs:     cfg.ConnectionsDenyCIDRs,
		AllowCIDRs:    cfg.ConnectionsAllowCIDRs,
		AllowLoopback: cfg.ConnectionsAllowLoopback,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid connection network settings: %w", err)
	}
	c.Dialects = dialect.NewRegistry()
	c.Connections, err = providers.New(c.Dialer, providers.Options{
		Dialects: c.Dialects,
		Secrets:  connections.SecretResolver{FileBaseDir: cfg.SecretFileBaseDir},
		SQLite:   sqlite.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite},
		Glue:     glue.Options{AllowAmbientCredentials: cfg.AllowAmbientAWSCredentials, AllowInsecureEndpoint: cfg.ConnectionsAllowLoopback},
	})
	if err != nil {
		return nil, fmt.Errorf("connection providers: %w", err)
	}
	// The SQLite engine confines files to the same root the provider uses
	// (docs/adr/0011).
	kinds, err := engines.NewRegistry(engines.Options{
		Dialects: c.Dialects,
		SQLite:   sqliteengine.Options{Root: cfg.SQLiteRoot, AllowWrite: cfg.SQLiteAllowWrite},
		Oracle:   oracle.Options{LOBMaxBytes: cfg.OracleLOBMaxBytes},
	})
	if err != nil {
		return nil, fmt.Errorf("engine kinds: %w", err)
	}
	c.Engines = queryengine.NewManager(c.Queries, c.Keyring, c.Connections, c.Dialer, kinds)

	if c.Storage, err = storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL); err != nil {
		return nil, err
	}
	c.Storage.SetMetrics(c.Metrics.Storage())

	c.TrinoAdmin = trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoAdminUser, obs.WrapHTTPClient(c.Dialer.HTTPClient(60*time.Second)))
	c.Binder = bindtrino.New(c.TrinoAdmin, c.Queries).WithRegistryKeyring(c.RegistryKeyring)
	c.Audit = audit.NewPostgres(c.Queries, c.Connections.SecretKeys(), c.Metrics.Audit().WriteFailed)
	return c, nil
}

// onClose registers f to run on Close, in reverse order of registration.
func (c *Core) onClose(f func()) { c.closers = append(c.closers, f) }

// Close releases the graph, last built first. It is safe to call twice.
func (c *Core) Close() {
	for i := len(c.closers) - 1; i >= 0; i-- {
		c.closers[i]()
	}
	c.closers = nil
}

// ErrNoService is returned by New when Options.Service is empty.
var ErrNoService = errors.New("app: Options.Service is required")
