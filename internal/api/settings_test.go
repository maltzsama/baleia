package api

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
)

// TestSettingsFromConfig: defaults are resolved once, so a zero config value
// reaches the handlers as the packaged default, and set values pass through.
func TestSettingsFromConfig(t *testing.T) {
	zero := SettingsFromConfig(&config.Config{})
	if zero.Auth.AccessTTL != config.DefaultAccessTokenTTL || zero.Auth.RefreshTTL != config.DefaultRefreshTokenTTL ||
		zero.Auth.ResetTTL != config.DefaultResetTokenTTL || zero.Auth.PasswordCost != config.DefaultBcryptCost {
		t.Errorf("auth defaults = %+v", zero.Auth)
	}
	if zero.Ops.MetadataSyncEvery != config.DefaultMetadataSyncInterval {
		t.Errorf("metadata sync = %v, want the default", zero.Ops.MetadataSyncEvery)
	}
	if zero.Execution.PolarisDefaultCredentials != nil {
		t.Error("no Polaris credentials unless both halves are set")
	}

	s := SettingsFromConfig(&config.Config{
		AccessTokenTTL: time.Minute, AgentsEnabled: true, AgentMaxRows: 7,
		PolarisClientID: "id", PolarisClientSecret: "secret", SMTPHost: "smtp", AuditExportURL: "s3://audit",
	})
	if s.Auth.AccessTTL != time.Minute || !s.Agents.Enabled || s.Agents.MaxRows != 7 ||
		s.Mail.SMTPHost != "smtp" || s.Ops.AuditExportURL != "s3://audit" {
		t.Errorf("settings = %+v", s)
	}
	if s.Execution.PolarisDefaultCredentials["client_id"] != "id" {
		t.Errorf("polaris = %v", s.Execution.PolarisDefaultCredentials)
	}
}

// TestSettings_ZeroValue: handlers built without configuration fall back to
// the defaults the nil config used to give.
func TestSettings_ZeroValue(t *testing.T) {
	h := &Handlers{}
	if h.metadataSyncEvery() != config.DefaultMetadataSyncInterval {
		t.Errorf("metadata sync = %v, want the default", h.metadataSyncEvery())
	}
	if h.agentsEnabled() {
		t.Error("agents must be off")
	}
	if h.reconcileMaxActions() != 0 {
		t.Error("reconcile max actions must defer to the bindings default")
	}
}
