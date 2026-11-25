package api

import (
	"cmp"
	"time"

	"github.com/galileostudio/baleia/deploy/opa"
	"github.com/galileostudio/baleia/internal/config"
)

// Settings is the part of the configuration the handlers read, resolved once
// at the wiring site (#363): defaults are already applied, so a handler never
// sees a zero lifetime. The zero Settings is what a handler built without
// configuration sees: no agents, no SMTP, no schedules, packaged ceilings.
type Settings struct {
	Auth        AuthSettings
	Mail        MailSettings
	Agents      AgentSettings
	Execution   ExecutionSettings
	Ops         OpsSettings
	Connections ConnectionSettings
	BIGateway   BIGatewaySettings
	DataPolicy  DataPolicySettings
}

// DataPolicySettings are the deployment's data policy settings (ADR 0028).
type DataPolicySettings struct {
	// Enabled is BALEIA_DATA_POLICIES=opa.
	Enabled bool
	// IdentityMode is BALEIA_TRINO_USER_IDENTITY, which the subject map
	// follows.
	IdentityMode string
	Lease        time.Duration
	// Trusted are the trusted Trino identities: never filtered or masked.
	Trusted []string
	// ExpectedDigest is the Rego digest engines must report.
	ExpectedDigest string
}

// BIGatewaySettings describe what the BI data-source file points at (ADR
// 0017): the listener must be on and have a public URL the BI tool can reach.
// The readiness fields report configuration without exposing the values
// themselves: the HMAC secret and the internal listener address stay hidden.
type BIGatewaySettings struct {
	Enabled bool
	URL     string
	// ListenerConfigured reports whether BALEIA_BI_GATEWAY_ADDR is set.
	ListenerConfigured bool
	// SecretConfigured reports whether BALEIA_BI_GATEWAY_SECRET is set.
	SecretConfigured bool
	// URLReady reports whether URL is an http(s) origin with no path.
	URLReady bool
	// MaxPerKey and MaxPerWorkspace are the running BI statement limits; 0
	// disables one.
	MaxPerKey       int
	MaxPerWorkspace int
}

// AuthSettings are the token and password settings.
type AuthSettings struct {
	JWTSecret    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	ResetTTL     time.Duration
	PasswordCost int
}

// MailSettings are the transactional e-mail settings and the links they carry.
type MailSettings struct {
	PublicURL     string
	DefaultLocale string
	SMTPHost      string
	SMTPPort      string
	SMTPUser      string
	SMTPPassword  string
	SMTPFrom      string
}

// AgentSettings are the agent switch, token lifetimes and ceilings (#60); a
// zero ceiling means the packaged default.
type AgentSettings struct {
	Enabled                bool
	TokenDefaultTTL        time.Duration
	TokenMaxTTL            time.Duration
	MaxRows                int
	MaxResultBytes         int
	MaxQueryRuntimeSeconds int
	MaxConcurrentQueries   int
	MaxQueriesPerHour      int
	MaxToolCallsPerMinute  int
}

// ExecutionSettings shape how queries run.
type ExecutionSettings struct {
	TrinoUserIdentity string
	StatementTimeout  time.Duration
	ExecutionTimeout  time.Duration
	// WorkloadEnabled records the workload class of new runs (#399).
	WorkloadEnabled bool
	// TrinoResourceGroupsEnvironment is the environment the managed Trino
	// resource groups are published under (#635).
	TrinoResourceGroupsEnvironment string
	// PolarisDefaultCredentials is nil unless both halves are configured.
	PolarisDefaultCredentials map[string]string
}

// OpsSettings are the schedules, reconciliation and platform details.
type OpsSettings struct {
	PluginVersion       string
	AuditExportURL      string
	SchedulesEnabled    bool
	MetadataSyncEvery   time.Duration
	ReconcileMaxActions int
	ReconcileInterval   time.Duration
	TemporalUIURL       string
	TemporalNamespace   string
}

// ConnectionSettings say who may create and edit connections.
type ConnectionSettings struct {
	// AdminsOnly is BALEIA_CONNECTIONS_CREATE=admins (#512, #610), the
	// default; false lets everyone.
	AdminsOnly bool
}

// SettingsFromConfig resolves the handler settings from the process config.
func SettingsFromConfig(c *config.Config) Settings {
	return Settings{
		Auth: AuthSettings{
			JWTSecret:    c.JWTSecret,
			AccessTTL:    c.AccessTTL(),
			RefreshTTL:   c.RefreshTTL(),
			ResetTTL:     c.ResetTTL(),
			PasswordCost: c.PasswordCost(),
		},
		Mail: MailSettings{
			PublicURL:     c.PublicURL,
			DefaultLocale: c.DefaultLocale,
			SMTPHost:      c.SMTPHost,
			SMTPPort:      c.SMTPPort,
			SMTPUser:      c.SMTPUser,
			SMTPPassword:  c.SMTPPassword,
			SMTPFrom:      c.SMTPFrom,
		},
		Agents: AgentSettings{
			Enabled:                c.AgentsEnabled,
			TokenDefaultTTL:        c.AgentTokenDefaultTTL,
			TokenMaxTTL:            c.AgentTokenMaxTTL,
			MaxRows:                c.AgentMaxRows,
			MaxResultBytes:         c.AgentMaxResultBytes,
			MaxQueryRuntimeSeconds: c.AgentMaxQueryRuntimeSeconds,
			MaxConcurrentQueries:   c.AgentMaxConcurrentQueries,
			MaxQueriesPerHour:      c.AgentMaxQueriesPerHour,
			MaxToolCallsPerMinute:  c.AgentMaxToolCallsPerMinute,
		},
		Execution: ExecutionSettings{
			TrinoUserIdentity:              c.TrinoUserIdentity,
			StatementTimeout:               c.StatementTimeout,
			ExecutionTimeout:               c.ExecutionTimeout,
			WorkloadEnabled:                c.WorkloadEnabled,
			TrinoResourceGroupsEnvironment: c.TrinoResourceGroupsEnvironment,
			PolarisDefaultCredentials:      c.PolarisDefaultCredentials(),
		},
		Ops: OpsSettings{
			PluginVersion:       c.PluginVersion,
			AuditExportURL:      c.AuditExportURL,
			SchedulesEnabled:    c.SchedulesEnabled,
			MetadataSyncEvery:   c.MetadataSyncEvery(),
			ReconcileMaxActions: c.ReconcileMaxActions,
			ReconcileInterval:   c.ReconcileInterval,
			TemporalUIURL:       c.TemporalUIURL,
			TemporalNamespace:   c.TemporalNamespace,
		},
		Connections: ConnectionSettings{AdminsOnly: c.ConnectionsCreate == "admins"},
		DataPolicy: DataPolicySettings{
			Enabled:        c.DataPoliciesEnabled(),
			IdentityMode:   c.TrinoUserIdentity,
			Lease:          c.DataPolicyLease,
			Trusted:        []string{c.TrinoAdminUser, c.TrinoServiceUser},
			ExpectedDigest: cmp.Or(c.DataPolicyRegoDigest, opa.Digest()),
		},
		BIGateway: BIGatewaySettings{
			Enabled:            c.BIGatewayAddr != "" && c.BIGatewayURL != "",
			URL:                c.BIGatewayURL,
			ListenerConfigured: c.BIGatewayAddr != "",
			SecretConfigured:   c.BIGatewaySecret != "",
			URLReady:           biGatewayPublicURLReady(c.BIGatewayURL),
			MaxPerKey:          c.BIMaxConcurrentPerKey,
			MaxPerWorkspace:    c.BIMaxConcurrentPerWorkspace,
		},
	}
}
