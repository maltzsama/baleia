package api

import (
	"time"

	"github.com/galileostudio/baleia/internal/config"
)

// Settings is the part of the configuration the handlers read, resolved once
// at the wiring site (#363): defaults are already applied, so a handler never
// sees a zero lifetime. The zero Settings is what a handler built without
// configuration sees: no agents, no SMTP, no schedules, packaged ceilings.
type Settings struct {
	Auth      AuthSettings
	Mail      MailSettings
	Agents    AgentSettings
	Execution ExecutionSettings
	Ops       OpsSettings
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
	// MaintenanceMinRetention is the retention floor projected into managed
	// Iceberg catalogs (#500).
	MaintenanceMinRetention time.Duration
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
			TrinoUserIdentity:         c.TrinoUserIdentity,
			StatementTimeout:          c.StatementTimeout,
			ExecutionTimeout:          c.ExecutionTimeout,
			PolarisDefaultCredentials: c.PolarisDefaultCredentials(),
		},
		Ops: OpsSettings{
			PluginVersion:           c.PluginVersion,
			AuditExportURL:          c.AuditExportURL,
			SchedulesEnabled:        c.SchedulesEnabled,
			MetadataSyncEvery:       c.MetadataSyncEvery(),
			ReconcileMaxActions:     c.ReconcileMaxActions,
			ReconcileInterval:       c.ReconcileInterval,
			MaintenanceMinRetention: c.MaintenanceMinRetention(),
		},
	}
}
