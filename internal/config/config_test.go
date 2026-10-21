package config

import (
	"testing"
	"time"
)

func TestValidateRejectsInsecureJWTSecret(t *testing.T) {
	for _, secret := range []string{"", DefaultJWTSecret, "short"} {
		c := &Config{JWTSecret: secret}
		if err := c.Validate(); err == nil {
			t.Fatalf("Validate accepted insecure JWT_SECRET %q", secret)
		}
	}
}

func TestValidateAcceptsCustomJWTSecret(t *testing.T) {
	c := &Config{JWTSecret: "a-long-random-value-not-the-default"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected a custom secret: %v", err)
	}
}

func TestValidateEncryptionKeys(t *testing.T) {
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
	if got := (&Config{PolarisClientID: "id"}).PolarisDefaultCredentials(); got != nil {
		t.Fatalf("only the id: %v, want nil", got)
	}
	got := (&Config{PolarisClientID: "id", PolarisClientSecret: "s"}).PolarisDefaultCredentials()
	if got["client_id"] != "id" || got["client_secret"] != "s" {
		t.Fatalf("got %v", got)
	}
}
