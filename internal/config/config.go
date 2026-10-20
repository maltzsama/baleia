package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultJWTSecret is the insecure placeholder shipped for local development.
// It signs JWTs, so ValidateSecret rejects it.
const DefaultJWTSecret = "change-me-in-production"

// MinJWTSecretLen is the minimum accepted length for JWT_SECRET. Short secrets
// are guessable.
const MinJWTSecretLen = 16

// MinEncryptionKeyLen is the minimum accepted length for BALEIA_ENCRYPTION_KEY.
const MinEncryptionKeyLen = 32

// Authentication token and password defaults (#51). A zero field means the
// default below, so a Config built without Load still gets safe values.
const (
	DefaultAccessTokenTTL  = 15 * time.Minute
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour
	DefaultResetTokenTTL   = 30 * time.Minute

	// DefaultBcryptCost is the production default. MinBcryptCost is bcrypt's
	// own lower bound; MaxBcryptCost keeps a bad configuration from making
	// every login pathologically slow.
	DefaultBcryptCost = 12
	MinBcryptCost     = 10
	MaxBcryptCost     = 31
)

type Config struct {
	Port         string
	DatabaseURL  string
	TemporalHost string
	PolarisURL   string
	TrinoURL     string
	// TrinoDBPassword is the password for the dedicated baleia_trino role, from
	// the same env var the Trino plugin reads. Empty means "leave it unchanged".
	TrinoDBPassword string
	// TrinoAdminUser runs catalog DDL, reconciliation and Lens maintenance.
	// Never used to run user SQL.
	TrinoAdminUser string
	// TrinoServiceUser is the fallback identity for executions when per-user
	// identity is disabled.
	TrinoServiceUser string
	// TrinoUserIdentity selects how a logged-in Baleia user maps to a Trino
	// identity for their queries: "uuid" (u_<uuid>) or "email".
	TrinoUserIdentity string
	// PluginVersion is the pinned baleia-trino-catalog-store release reported
	// by the system status (#291).
	PluginVersion string
	JWTSecret     string
	// AccessTokenTTL, RefreshTokenTTL and ResetTokenTTL configure token
	// lifetimes (#51); zero means the default.
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	ResetTokenTTL   time.Duration
	// BcryptCost is the password-hashing cost (#51); zero means the default.
	BcryptCost int
	// EncryptionKey seals connection credentials at rest. It is deliberately
	// separate from JWTSecret so rotating the signing secret never touches stored
	// credentials. EncryptionKeyPrevious is set only during a key rotation.
	EncryptionKey         string
	EncryptionKeyPrevious string
	// RegistryKey seals catalog-registry values at rest (Phase B of #31 / D4).
	// A dedicated key, never derived from JWTSecret or EncryptionKey. Empty
	// disables Phase B encryption (rows stay plaintext); RegistryKeyPrevious is
	// set only during a key rotation.
	RegistryKey         string
	RegistryKeyPrevious string
	PublicURL           string
	SMTPHost            string
	SMTPPort            string
	SMTPUser            string
	SMTPPassword        string
	SMTPFrom            string
	// DefaultLocale is the fallback for localized e-mails when the recipient
	// has no users.locale (#67).
	DefaultLocale string
	S3Endpoint    string
	S3AccessKey   string
	S3SecretKey   string
	S3Bucket      string
	S3UseSSL      bool
	// StatementTimeout bounds a single statement execution. The script
	// activity enforces it per statement and passes it to the engine (Trino:
	// query_max_execution_time) so the engine enforces it too.
	StatementTimeout time.Duration
	// ExecutionTimeout caps a whole script (every statement of an
	// execution, run in one activity, #27).
	ExecutionTimeout time.Duration
	// MetadataSyncInterval is how often every catalog's metadata snapshot is
	// refreshed (#17).
	MetadataSyncInterval time.Duration
	// ReconcileMaxActions bounds the changes of one reconcile run (#37);
	// ReconcileInterval schedules runs, none when 0 (the default).
	ReconcileMaxActions int
	ReconcileInterval   time.Duration
	// ResultMaxRows / ResultMaxBytes cap one statement's result. When a cap is
	// hit the remaining query is killed and the result is marked truncated.
	ResultMaxRows  int64
	ResultMaxBytes int64
	// SchedulesEnabled drives whether the worker ensures its recurring Temporal
	// Schedules at boot. Disabled for tests and local dev that lack Temporal.
	SchedulesEnabled bool
	RateLimitRPS     int
	RateLimitBurst   int
	// Connections* configure the dialer every connection provider uses to
	// reach user-supplied hosts (docs/adr/0011). Loopback, link-local and
	// cloud metadata addresses are always denied unless loopback is allowed
	// for local development.
	ConnectionsDenyCIDRs     []string
	ConnectionsAllowCIDRs    []string
	ConnectionsAllowLoopback bool
	// SQLiteRoot confines SQLite connections to files under it; empty
	// disables the SQLite provider. SQLiteAllowWrite lets a connection that
	// asks for it open its file read-write.
	SQLiteRoot       string
	SQLiteAllowWrite bool
	// AllowAmbientAWSCredentials lets a Glue connection with no explicit keys
	// or role use the host's default AWS credential chain (#86). Off by
	// default: any connection owner could otherwise use the platform's IAM role.
	AllowAmbientAWSCredentials bool
	// Metrics (#87): /metrics on the API (MetricsEnabled, optional bearer
	// MetricsToken) and the worker's own listener (WorkerMetricsAddr).
	MetricsEnabled    bool
	MetricsToken      string
	WorkerMetricsAddr string
	// TracingEnabled installs the OpenTelemetry tracer provider (#47). Off by
	// default; the OTLP endpoint and service name come from the standard OTEL_*
	// environment variables.
	TracingEnabled bool
	// AuditExport* ship audit events to an OpenSearch-compatible store (#50).
	// URL empty disables the export.
	AuditExportURL      string
	AuditExportIndex    string
	AuditExportAuth     string
	AuditExportUser     string
	AuditExportPassword string
	AuditExportToken    string
	AuditExportCAFile   string
	AuditExportBatch    int
	AuditExportInterval time.Duration
	// Baleia's database pool; DBMaxConns 0 means the process default (api 20,
	// worker 10).
	DBMaxConns        int
	DBMinConns        int
	DBMaxConnLifetime time.Duration
	// AgentsEnabled mounts the agent management API and /mcp (#59). Off by
	// default: no agent token exists until an administrator enables it.
	AgentsEnabled bool
	// AgentTokenDefaultTTL / AgentTokenMaxTTL bound the `expires_in_days`
	// accepted when a user mints an agent token (#59).
	AgentTokenDefaultTTL time.Duration
	AgentTokenMaxTTL     time.Duration
	// Agent policy ceilings (#60). A stored policy above a ceiling is rejected
	// on save and clamped at evaluation time.
	AgentMaxRows                int
	AgentMaxResultBytes         int
	AgentMaxQueryRuntimeSeconds int
	AgentMaxConcurrentQueries   int
	AgentMaxQueriesPerHour      int
	AgentMaxToolCallsPerMinute  int
	// MCPAllowedOrigins is the Origin allow-list for /mcp; empty rejects any
	// request that carries an Origin header (native MCP clients omit it).
	MCPAllowedOrigins []string
}

func Load() *Config {
	return &Config{
		Port:                  getEnv("PORT", "8080"),
		DatabaseURL:           getEnv("DATABASE_URL", "postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable"),
		TemporalHost:          getEnv("TEMPORAL_HOST", "localhost:7233"),
		PolarisURL:            getEnv("POLARIS_URL", "http://localhost:8181"),
		TrinoURL:              getEnv("TRINO_URL", "http://localhost:8088"),
		TrinoDBPassword:       getEnv("BALEIA_TRINO_DB_PASSWORD", ""),
		TrinoAdminUser:        getEnv("BALEIA_TRINO_ADMIN_USER", "baleia_admin"),
		TrinoServiceUser:      getEnv("BALEIA_TRINO_SERVICE_USER", "baleia"),
		TrinoUserIdentity:     getEnv("BALEIA_TRINO_USER_IDENTITY", "uuid"),
		PluginVersion:         getEnv("BALEIA_PLUGIN_VERSION", "0.9.2"),
		JWTSecret:             getEnv("JWT_SECRET", DefaultJWTSecret),
		AccessTokenTTL:        getEnvDuration("BALEIA_ACCESS_TOKEN_TTL", DefaultAccessTokenTTL),
		RefreshTokenTTL:       getEnvDuration("BALEIA_REFRESH_TOKEN_TTL", DefaultRefreshTokenTTL),
		ResetTokenTTL:         getEnvDuration("BALEIA_RESET_TOKEN_TTL", DefaultResetTokenTTL),
		BcryptCost:            getEnvInt("BALEIA_BCRYPT_COST", DefaultBcryptCost),
		EncryptionKey:         getEnv("BALEIA_ENCRYPTION_KEY", ""),
		EncryptionKeyPrevious: getEnv("BALEIA_ENCRYPTION_KEY_PREVIOUS", ""),
		RegistryKey:           getEnv("BALEIA_REGISTRY_KEY", ""),
		RegistryKeyPrevious:   getEnv("BALEIA_REGISTRY_KEY_PREVIOUS", ""),
		PublicURL:             getEnv("PUBLIC_URL", "http://localhost:8080"),
		SMTPHost:              getEnv("SMTP_HOST", ""),
		SMTPPort:              getEnv("SMTP_PORT", ""),
		SMTPUser:              getEnv("SMTP_USER", ""),
		SMTPPassword:          getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:              getEnv("SMTP_FROM", ""),
		DefaultLocale:         getEnv("BALEIA_DEFAULT_LOCALE", "en"),
		S3Endpoint:            getEnv("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:           getEnv("S3_ACCESS_KEY", "rustfsadmin"),
		S3SecretKey:           getEnv("S3_SECRET_KEY", "rustfsadmin"),
		S3Bucket:              getEnv("S3_BUCKET", "baleia"),
		S3UseSSL:              getEnv("S3_USE_SSL", "false") == "true",
		StatementTimeout:      getEnvDuration("BALEIA_STATEMENT_TIMEOUT", 30*time.Minute),
		ExecutionTimeout:      getEnvDuration("BALEIA_EXECUTION_TIMEOUT", 2*time.Hour),
		MetadataSyncInterval:  getEnvDuration("BALEIA_METADATA_SYNC_INTERVAL", 15*time.Minute),
		ReconcileMaxActions:   getEnvInt("BALEIA_RECONCILE_MAX_ACTIONS", 20),
		ReconcileInterval:     getEnvDuration("BALEIA_RECONCILE_INTERVAL", 0),
		ResultMaxRows:         getEnvInt64("BALEIA_RESULT_MAX_ROWS", 1000000),
		ResultMaxBytes:        getEnvInt64("BALEIA_RESULT_MAX_BYTES", 536870912),
		SchedulesEnabled:      getEnv("BALEIA_SCHEDULES_ENABLED", "true") == "true",
		RateLimitRPS:          getEnvInt("RATE_LIMIT_RPS", 60),
		RateLimitBurst:        getEnvInt("RATE_LIMIT_BURST", 10),

		ConnectionsDenyCIDRs:        splitList(getEnv("BALEIA_CONNECTIONS_DENY_CIDRS", "")),
		ConnectionsAllowCIDRs:       splitList(getEnv("BALEIA_CONNECTIONS_ALLOW_CIDRS", "")),
		ConnectionsAllowLoopback:    getEnv("BALEIA_CONNECTIONS_ALLOW_LOOPBACK", "false") == "true",
		SQLiteRoot:                  getEnv("BALEIA_SQLITE_ROOT", ""),
		SQLiteAllowWrite:            getEnv("BALEIA_SQLITE_ALLOW_WRITE", "false") == "true",
		AllowAmbientAWSCredentials:  getEnv("BALEIA_ALLOW_AMBIENT_AWS_CREDENTIALS", "false") == "true",
		MetricsEnabled:              getEnv("BALEIA_METRICS_ENABLED", "true") == "true",
		MetricsToken:                getEnv("BALEIA_METRICS_TOKEN", ""),
		WorkerMetricsAddr:           getEnv("BALEIA_WORKER_METRICS_ADDR", ":9090"),
		TracingEnabled:              getEnv("BALEIA_TRACING_ENABLED", "false") == "true",
		AgentsEnabled:               getEnv("BALEIA_AGENTS_ENABLED", "false") == "true",
		AgentTokenDefaultTTL:        getEnvDuration("BALEIA_AGENT_TOKEN_DEFAULT_TTL", 30*24*time.Hour),
		AgentTokenMaxTTL:            getEnvDuration("BALEIA_AGENT_TOKEN_MAX_TTL", 90*24*time.Hour),
		AgentMaxRows:                getEnvInt("BALEIA_AGENT_MAX_ROWS", 5000),
		AgentMaxResultBytes:         getEnvInt("BALEIA_AGENT_MAX_RESULT_BYTES", 1<<20),
		AgentMaxQueryRuntimeSeconds: getEnvInt("BALEIA_AGENT_MAX_QUERY_RUNTIME_SECONDS", 900),
		AgentMaxConcurrentQueries:   getEnvInt("BALEIA_AGENT_MAX_CONCURRENT_QUERIES", 10),
		AgentMaxQueriesPerHour:      getEnvInt("BALEIA_AGENT_MAX_QUERIES_PER_HOUR", 2000),
		AgentMaxToolCallsPerMinute:  getEnvInt("BALEIA_AGENT_MAX_TOOL_CALLS_PER_MINUTE", 600),
		MCPAllowedOrigins:           splitList(getEnv("BALEIA_MCP_ALLOWED_ORIGINS", "")),
		AuditExportURL:              getEnv("BALEIA_AUDIT_EXPORT_URL", ""),
		AuditExportIndex:            getEnv("BALEIA_AUDIT_EXPORT_INDEX", "baleia-audit"),
		AuditExportAuth:             getEnv("BALEIA_AUDIT_EXPORT_AUTH", "none"),
		AuditExportUser:             getEnv("BALEIA_AUDIT_EXPORT_USER", ""),
		AuditExportPassword:         getEnv("BALEIA_AUDIT_EXPORT_PASSWORD", ""),
		AuditExportToken:            getEnv("BALEIA_AUDIT_EXPORT_TOKEN", ""),
		AuditExportCAFile:           getEnv("BALEIA_AUDIT_EXPORT_CA_FILE", ""),
		AuditExportBatch:            getEnvInt("BALEIA_AUDIT_EXPORT_BATCH", 500),
		AuditExportInterval:         getEnvDuration("BALEIA_AUDIT_EXPORT_INTERVAL", 30*time.Second),
		DBMaxConns:                  getEnvInt("BALEIA_DB_MAX_CONNS", 0),
		DBMinConns:                  getEnvInt("BALEIA_DB_MIN_CONNS", 2),
		DBMaxConnLifetime:           getEnvDuration("BALEIA_DB_MAX_CONN_LIFETIME", time.Hour),
	}
}

// Validate rejects configuration that must not reach production: a missing or
// default JWT_SECRET would let anyone with the database recover stored
// credentials and forge tokens.
// ValidateSecret reports why s is unacceptable as JWT_SECRET, or nil when it is
// safe for signing and credential encryption.
func ValidateSecret(s string) error {
	if s == "" || s == DefaultJWTSecret {
		return errors.New("JWT_SECRET must be set to a non-default, high-entropy value")
	}
	if len(s) < MinJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", MinJWTSecretLen)
	}
	return nil
}

// ValidateEncryptionKeys checks the credential-encryption keys when set. An
// absent key is not an error here: whether one is required depends on whether
// the database already holds credentials (see bootstrap.LoadKeyring).
func (c *Config) ValidateEncryptionKeys() error {
	if c.EncryptionKey == "" {
		if c.EncryptionKeyPrevious != "" {
			return errors.New("BALEIA_ENCRYPTION_KEY_PREVIOUS is set without BALEIA_ENCRYPTION_KEY")
		}
		return nil
	}
	if len(c.EncryptionKey) < MinEncryptionKeyLen {
		return fmt.Errorf("BALEIA_ENCRYPTION_KEY must be at least %d characters", MinEncryptionKeyLen)
	}
	if c.EncryptionKey == c.JWTSecret {
		return errors.New("BALEIA_ENCRYPTION_KEY must differ from JWT_SECRET")
	}
	if c.EncryptionKeyPrevious != "" && len(c.EncryptionKeyPrevious) < MinEncryptionKeyLen {
		return fmt.Errorf("BALEIA_ENCRYPTION_KEY_PREVIOUS must be at least %d characters", MinEncryptionKeyLen)
	}
	return nil
}

// ValidateRegistryKey checks the catalog-registry encryption keys when set.
// An absent key is valid: Phase B (#200) is opt-in, and rows stay plaintext
// until an operator enables it.
func (c *Config) ValidateRegistryKey() error {
	if c.RegistryKey == "" {
		if c.RegistryKeyPrevious != "" {
			return errors.New("BALEIA_REGISTRY_KEY_PREVIOUS is set without BALEIA_REGISTRY_KEY")
		}
		return nil
	}
	if len(c.RegistryKey) < MinEncryptionKeyLen {
		return fmt.Errorf("BALEIA_REGISTRY_KEY must be at least %d characters", MinEncryptionKeyLen)
	}
	if c.RegistryKey == c.JWTSecret {
		return errors.New("BALEIA_REGISTRY_KEY must differ from JWT_SECRET")
	}
	if c.RegistryKey == c.EncryptionKey {
		return errors.New("BALEIA_REGISTRY_KEY must differ from BALEIA_ENCRYPTION_KEY")
	}
	if c.RegistryKeyPrevious != "" && len(c.RegistryKeyPrevious) < MinEncryptionKeyLen {
		return fmt.Errorf("BALEIA_REGISTRY_KEY_PREVIOUS must be at least %d characters", MinEncryptionKeyLen)
	}
	return nil
}

// Validate rejects configuration that must not reach production.
func (c *Config) Validate() error {
	if err := ValidateSecret(c.JWTSecret); err != nil {
		return err
	}
	if err := c.ValidateEncryptionKeys(); err != nil {
		return err
	}
	if err := c.ValidateRegistryKey(); err != nil {
		return err
	}
	if c.ReconcileInterval != 0 && c.ReconcileInterval < 5*time.Minute {
		return fmt.Errorf("BALEIA_RECONCILE_INTERVAL must be at least 5m (got %s)", c.ReconcileInterval)
	}
	if c.MetadataSyncInterval < 0 || (c.MetadataSyncInterval > 0 && c.MetadataSyncInterval < 5*time.Minute) {
		return fmt.Errorf("BALEIA_METADATA_SYNC_INTERVAL must be at least 5m (got %s)", c.MetadataSyncInterval)
	}
	if c.StatementTimeout != 0 && c.StatementTimeout < time.Minute {
		return fmt.Errorf("BALEIA_STATEMENT_TIMEOUT must be at least 1m (got %s)", c.StatementTimeout)
	}
	if c.ExecutionTimeout != 0 && c.ExecutionTimeout < time.Minute {
		return fmt.Errorf("BALEIA_EXECUTION_TIMEOUT must be at least 1m (got %s)", c.ExecutionTimeout)
	}
	for _, ttl := range []struct {
		name string
		v    time.Duration
	}{
		{"BALEIA_ACCESS_TOKEN_TTL", c.AccessTokenTTL},
		{"BALEIA_REFRESH_TOKEN_TTL", c.RefreshTokenTTL},
		{"BALEIA_RESET_TOKEN_TTL", c.ResetTokenTTL},
	} {
		if ttl.v < 0 {
			return fmt.Errorf("%s must be positive (got %s)", ttl.name, ttl.v)
		}
	}
	if c.BcryptCost != 0 && (c.BcryptCost < MinBcryptCost || c.BcryptCost > MaxBcryptCost) {
		return fmt.Errorf("BALEIA_BCRYPT_COST must be between %d and %d (got %d)", MinBcryptCost, MaxBcryptCost, c.BcryptCost)
	}
	return nil
}

// AccessTTL is the access-token lifetime; zero means DefaultAccessTokenTTL.
func (c *Config) AccessTTL() time.Duration {
	if c.AccessTokenTTL <= 0 {
		return DefaultAccessTokenTTL
	}
	return c.AccessTokenTTL
}

// RefreshTTL is the refresh-token lifetime; zero means DefaultRefreshTokenTTL.
func (c *Config) RefreshTTL() time.Duration {
	if c.RefreshTokenTTL <= 0 {
		return DefaultRefreshTokenTTL
	}
	return c.RefreshTokenTTL
}

// ResetTTL is the password-reset token lifetime; zero means
// DefaultResetTokenTTL.
func (c *Config) ResetTTL() time.Duration {
	if c.ResetTokenTTL <= 0 {
		return DefaultResetTokenTTL
	}
	return c.ResetTokenTTL
}

// PasswordCost is the bcrypt cost; zero means DefaultBcryptCost.
func (c *Config) PasswordCost() int {
	if c.BcryptCost <= 0 {
		return DefaultBcryptCost
	}
	return c.BcryptCost
}

// splitList splits a comma-separated list, ignoring blanks.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// PoolSize is DBMaxConns, or the process default when unset.
func (c *Config) PoolSize(processDefault int) int32 {
	if c.DBMaxConns > 0 {
		return int32(c.DBMaxConns)
	}
	return int32(processDefault)
}

// DefaultMetadataSyncInterval is used when BALEIA_METADATA_SYNC_INTERVAL is
// 0 or unset.
const DefaultMetadataSyncInterval = 15 * time.Minute

// MetadataSyncEvery is the metadata-sync schedule's interval: 0 means the
// default, never "no interval" (a schedule needs one).
func (c *Config) MetadataSyncEvery() time.Duration {
	if c.MetadataSyncInterval <= 0 {
		return DefaultMetadataSyncInterval
	}
	return c.MetadataSyncInterval
}
