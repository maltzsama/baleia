package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsInsecureJWTSecret(t *testing.T) {
	t.Parallel()
	for _, secret := range []string{"", DefaultJWTSecret, "short"} {
		c := &Config{JWTSecret: secret}
		if err := c.Validate(); err == nil {
			t.Fatalf("Validate accepted insecure JWT_SECRET %q", secret)
		}
	}
}

func TestValidateAcceptsCustomJWTSecret(t *testing.T) {
	t.Parallel()
	c := &Config{JWTSecret: "a-long-random-value-not-the-default"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected a custom secret: %v", err)
	}
}

func TestValidateEncryptionKeys(t *testing.T) {
	t.Parallel()
	const jwt = "a-long-random-value-not-the-default"
	key := "k-0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"unset is allowed (fresh install)", Config{JWTSecret: jwt}, true},
		{"valid", Config{JWTSecret: jwt, EncryptionKey: key}, true},
		{"valid with previous", Config{JWTSecret: jwt, EncryptionKey: key, EncryptionKeyPrevious: key + "-old"}, true},
		{"too short", Config{JWTSecret: jwt, EncryptionKey: "short"}, false},
		{"equals the JWT secret", Config{JWTSecret: key, EncryptionKey: key}, false},
		{"previous without current", Config{JWTSecret: jwt, EncryptionKeyPrevious: key}, false},
		{"previous too short", Config{JWTSecret: jwt, EncryptionKey: key, EncryptionKeyPrevious: "short"}, false},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestValidateRegistryKey(t *testing.T) {
	t.Parallel()
	const jwt = "a-long-random-value-not-the-default"
	key := "r-0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"unset is allowed (Phase A)", Config{JWTSecret: jwt}, true},
		{"valid", Config{JWTSecret: jwt, RegistryKey: key}, true},
		{"valid with previous", Config{JWTSecret: jwt, RegistryKey: key, RegistryKeyPrevious: key + "-old"}, true},
		{"too short", Config{JWTSecret: jwt, RegistryKey: "short"}, false},
		{"equals the JWT secret", Config{JWTSecret: key, RegistryKey: key}, false},
		{"equals the encryption key", Config{JWTSecret: jwt, EncryptionKey: key, RegistryKey: key}, false},
		{"previous without current", Config{JWTSecret: jwt, RegistryKeyPrevious: key}, false},
		{"previous too short", Config{JWTSecret: jwt, RegistryKey: key, RegistryKeyPrevious: "short"}, false},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

//nolint:paralleltest // t.Setenv: the environment is process-wide.
func TestConfig_TokenTTLDefaults(t *testing.T) {
	// A Config built without Load still gets safe defaults.
	c := &Config{}
	if got := c.AccessTTL(); got != DefaultAccessTokenTTL {
		t.Errorf("AccessTTL() = %s, want %s", got, DefaultAccessTokenTTL)
	}
	if got := c.RefreshTTL(); got != DefaultRefreshTokenTTL {
		t.Errorf("RefreshTTL() = %s, want %s", got, DefaultRefreshTokenTTL)
	}
	if got := c.ResetTTL(); got != DefaultResetTokenTTL {
		t.Errorf("ResetTTL() = %s, want %s", got, DefaultResetTokenTTL)
	}
	if got := c.PasswordCost(); got != DefaultBcryptCost {
		t.Errorf("PasswordCost() = %d, want %d", got, DefaultBcryptCost)
	}

	// Defaults are 15m / 30d / 30m / 12 when the environment leaves them unset.
	for _, key := range []string{
		"BALEIA_ACCESS_TOKEN_TTL", "BALEIA_REFRESH_TOKEN_TTL",
		"BALEIA_RESET_TOKEN_TTL", "BALEIA_BCRYPT_COST",
	} {
		t.Setenv(key, "")
	}
	loaded := Load()
	if loaded.AccessTTL() != 15*time.Minute {
		t.Errorf("default access TTL = %s, want 15m", loaded.AccessTTL())
	}
	if loaded.RefreshTTL() != 30*24*time.Hour {
		t.Errorf("default refresh TTL = %s, want 30d", loaded.RefreshTTL())
	}
	if loaded.ResetTTL() != 30*time.Minute {
		t.Errorf("default reset TTL = %s, want 30m", loaded.ResetTTL())
	}
	if loaded.PasswordCost() != 12 {
		t.Errorf("default bcrypt cost = %d, want 12", loaded.PasswordCost())
	}
}

func TestConfig_BcryptCostMin(t *testing.T) {
	t.Parallel()
	const jwt = "a-long-random-value-not-the-default"
	for _, cost := range []int{9, 32} {
		if err := (&Config{JWTSecret: jwt, BcryptCost: cost}).Validate(); err == nil {
			t.Errorf("Validate accepted bcrypt cost %d", cost)
		}
	}
	for _, cost := range []int{0, MinBcryptCost, DefaultBcryptCost, MaxBcryptCost} {
		if err := (&Config{JWTSecret: jwt, BcryptCost: cost}).Validate(); err != nil {
			t.Errorf("Validate rejected bcrypt cost %d: %v", cost, err)
		}
	}
}

func TestMetadataSyncEvery(t *testing.T) {
	t.Parallel()
	for in, want := range map[time.Duration]time.Duration{0: DefaultMetadataSyncInterval, 30 * time.Minute: 30 * time.Minute} {
		if got := (&Config{MetadataSyncInterval: in}).MetadataSyncEvery(); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	const jwt = "a-long-random-value-not-the-default"
	for _, bad := range []time.Duration{-time.Minute, time.Minute} {
		if err := (&Config{JWTSecret: jwt, MetadataSyncInterval: bad}).Validate(); err == nil {
			t.Errorf("Validate accepted interval %s", bad)
		}
	}
}

// TestPolarisDefaultCredentials: the polaris fallback needs both values.
func TestPolarisDefaultCredentials(t *testing.T) {
	t.Parallel()
	if got := (&Config{PolarisClientID: "id"}).PolarisDefaultCredentials(); got != nil {
		t.Fatalf("only the id: %v, want nil", got)
	}
	got := (&Config{PolarisClientID: "id", PolarisClientSecret: "s"}).PolarisDefaultCredentials()
	if got["client_id"] != "id" || got["client_secret"] != "s" {
		t.Fatalf("got %v", got)
	}
}

// TestConfig_SlogLevel: BALEIA_LOG_LEVEL takes the slog level names, in any
// case, and anything else fails Validate (#379).
func TestConfig_SlogLevel(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "WARN": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := (&Config{LogLevel: raw}).SlogLevel(); err != nil || got != want {
			t.Errorf("SlogLevel(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := (&Config{LogLevel: "verbose"}).SlogLevel(); err == nil {
		t.Error("SlogLevel accepted verbose")
	}
	c := Load()
	c.JWTSecret = strings.Repeat("s", 40)
	c.LogLevel = "verbose"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "BALEIA_LOG_LEVEL") {
		t.Errorf("Validate = %v, want a BALEIA_LOG_LEVEL error", err)
	}
}

// TestConfig_TrinoEventsToken: the query events endpoint (#406) takes a
// token as long as the JWT secret, or none.
func TestConfig_TrinoEventsToken(t *testing.T) {
	t.Parallel()
	base := Config{JWTSecret: strings.Repeat("s", MinJWTSecretLen)}
	short, long := base, base
	short.TrinoEventsToken = "short"
	long.TrinoEventsToken = strings.Repeat("t", MinJWTSecretLen)
	if err := short.Validate(); err == nil || !strings.Contains(err.Error(), "BALEIA_TRINO_EVENTS_TOKEN") {
		t.Errorf("short token = %v, want a BALEIA_TRINO_EVENTS_TOKEN error", err)
	}
	if err := long.Validate(); err != nil {
		t.Errorf("long token = %v", err)
	}
	if err := base.Validate(); err != nil {
		t.Errorf("no token = %v", err)
	}
}

// TestConfig_TrinoPassword (#646): a password needs HTTPS and the keyring.
func TestConfig_TrinoPassword(t *testing.T) {
	t.Parallel()
	base := Config{JWTSecret: strings.Repeat("s", MinJWTSecretLen), TrinoURL: "https://trino:8443", TrinoPassword: "pw", EncryptionKey: strings.Repeat("k", MinEncryptionKeyLen)}
	plain, noKey := base, base
	plain.TrinoURL = "http://trino:8080"
	noKey.EncryptionKey = ""
	for name, c := range map[string]Config{"http": plain, "no keyring": noKey} {
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "BALEIA_TRINO_PASSWORD") {
			t.Errorf("%s = %v, want a BALEIA_TRINO_PASSWORD error", name, err)
		}
	}
	if err := base.Validate(); err != nil {
		t.Errorf("https with keyring = %v", err)
	}
}

func TestConfig_TrinoPasswordNeedsHost(t *testing.T) {
	t.Parallel()
	c := Config{JWTSecret: strings.Repeat("s", MinJWTSecretLen), TrinoURL: "https://", TrinoPassword: "pw", EncryptionKey: strings.Repeat("k", MinEncryptionKeyLen)}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "BALEIA_TRINO_PASSWORD") {
		t.Errorf("no host = %v", err)
	}
}

func TestCatalogScopeEnforced(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version string
		want    bool
	}{
		{"0.10.0", false},
		{"0.10.1", false},
		{"0.10.2", true},
		{"0.10.2+trino484", true},
		{"0.11.0", true},
		{"1.0.0", true},
	}
	for _, tc := range cases {
		c := &Config{PluginVersion: tc.version}
		if got := c.CatalogScopeEnforced(); got != tc.want {
			t.Errorf("CatalogScopeEnforced(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestCatalogScopeWarning(t *testing.T) {
	t.Parallel()
	if got := (&Config{PluginVersion: "0.10.2"}).CatalogScopeWarning(); got != "" {
		t.Errorf("enforced version warns: %q", got)
	}
	warn := (&Config{PluginVersion: "0.10.0"}).CatalogScopeWarning()
	if warn == "" || !strings.Contains(warn, "0.10.2") {
		t.Errorf("old version should warn about 0.10.2: %q", warn)
	}
	refuse := (&Config{PluginVersion: "0.10.0", RequireCatalogScope: true}).CatalogScopeWarning()
	if refuse == "" || !strings.Contains(refuse, "refused") {
		t.Errorf("require flag should refuse: %q", refuse)
	}
}

// TestConfig_AnalyticsUmami: the tracker is off by default and is only checked
// when it is on, so a typo fails at startup instead of dropping the analytics.
func TestConfig_AnalyticsUmami(t *testing.T) {
	t.Parallel()
	const id = "4cff4cb4-3fb0-4c64-b9c8-275ddc250873"
	tests := []struct {
		name    string
		id, url string
		wantErr string
	}{
		{name: "off by default"},
		{name: "off ignores the url", url: "not a url"},
		{name: "cloud script", id: id, url: DefaultUmamiScriptURL},
		{name: "self-hosted http script", id: id, url: "http://umami.internal:3000/script.js"},
		{name: "id is not a uuid", id: "demo", url: DefaultUmamiScriptURL, wantErr: "BALEIA_ANALYTICS_UMAMI_WEBSITE_ID"},
		{name: "id in another uuid form", id: "urn:uuid:" + id, url: DefaultUmamiScriptURL, wantErr: "BALEIA_ANALYTICS_UMAMI_WEBSITE_ID"},
		{name: "id without hyphens", id: strings.ReplaceAll(id, "-", ""), url: DefaultUmamiScriptURL, wantErr: "BALEIA_ANALYTICS_UMAMI_WEBSITE_ID"},
		{name: "empty script url", id: id, wantErr: "BALEIA_ANALYTICS_UMAMI_SCRIPT_URL"},
		{name: "relative script url", id: id, url: "/script.js", wantErr: "BALEIA_ANALYTICS_UMAMI_SCRIPT_URL"},
		{name: "javascript scheme", id: id, url: "javascript:alert(1)", wantErr: "BALEIA_ANALYTICS_UMAMI_SCRIPT_URL"},
		{name: "protocol-relative url", id: id, url: "//evil.example/script.js", wantErr: "BALEIA_ANALYTICS_UMAMI_SCRIPT_URL"},
		{name: "credentials in the url", id: id, url: "https://user:secret@umami.example/script.js", wantErr: "BALEIA_ANALYTICS_UMAMI_SCRIPT_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Config{
				JWTSecret:               strings.Repeat("s", MinJWTSecretLen),
				AnalyticsUmamiWebsiteID: tt.id,
				AnalyticsUmamiScriptURL: tt.url,
			}
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("Validate() error leaks the URL credentials: %v", err)
			}
		})
	}
}

// TestLoad_AnalyticsUmamiDefaults: unset means off, and the script defaults to
// Umami Cloud's so only the website id is needed to turn it on.
func TestLoad_AnalyticsUmamiDefaults(t *testing.T) {
	t.Setenv("BALEIA_ANALYTICS_UMAMI_WEBSITE_ID", "")
	t.Setenv("BALEIA_ANALYTICS_UMAMI_SCRIPT_URL", "")
	c := Load()
	if c.AnalyticsUmamiWebsiteID != "" {
		t.Errorf("website id = %q, want it off", c.AnalyticsUmamiWebsiteID)
	}
	if c.AnalyticsUmamiScriptURL != DefaultUmamiScriptURL {
		t.Errorf("script url = %q, want %q", c.AnalyticsUmamiScriptURL, DefaultUmamiScriptURL)
	}
}

func TestConfig_PartitionDaysAhead(t *testing.T) {
	t.Setenv("BALEIA_PARTITION_DAYS_AHEAD", "")
	if got := Load().PartitionDaysAhead; got != 14 {
		t.Fatalf("default PartitionDaysAhead = %d, want 14", got)
	}
	t.Setenv("BALEIA_PARTITION_DAYS_AHEAD", "30")
	if got := Load().PartitionDaysAhead; got != 30 {
		t.Fatalf("PartitionDaysAhead = %d, want 30", got)
	}
	cfg := &Config{JWTSecret: strings.Repeat("s", MinJWTSecretLen), PartitionDaysAhead: -1}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "BALEIA_PARTITION_DAYS_AHEAD") {
		t.Fatalf("Validate() = %v, want an error naming BALEIA_PARTITION_DAYS_AHEAD", err)
	}
}
