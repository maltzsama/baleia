package auth

import (
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/identity"
)

// TestOIDCConfigValidate pins the rules the admin API relies on (#259).
func TestOIDCConfigValidate(t *testing.T) {
	t.Parallel()
	valid := OIDCProviderConfig{
		IssuerURL:    "https://idp.example.test",
		ClientID:     "baleia",
		ClientSecret: "secret",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := map[string]func(*OIDCProviderConfig){
		"plain http issuer":       func(c *OIDCProviderConfig) { c.IssuerURL = "http://idp.example.test" },
		"issuer without scheme":   func(c *OIDCProviderConfig) { c.IssuerURL = "idp.example.test" },
		"issuer with credentials": func(c *OIDCProviderConfig) { c.IssuerURL = "https://user:pass@idp.example.test" },
		"empty client id":         func(c *OIDCProviderConfig) { c.ClientID = " " },
		"empty secret":            func(c *OIDCProviderConfig) { c.ClientSecret = "" },
		"auto create":             func(c *OIDCProviderConfig) { c.AutoCreate = true },
		"empty domain":            func(c *OIDCProviderConfig) { c.AllowedEmailDomains = []string{"example.test", " "} },
		"unknown role":            func(c *OIDCProviderConfig) { c.DefaultRole = "owner" },
		"empty role group":        func(c *OIDCProviderConfig) { c.RoleMapping = []identity.RoleMapping{{Role: "admin"}} },
		"empty team id":           func(c *OIDCProviderConfig) { c.TeamMapping = []identity.TeamMapping{{Group: "g", TeamRole: "member"}} },
		"unknown team role": func(c *OIDCProviderConfig) {
			c.TeamMapping = []identity.TeamMapping{{Group: "g", TeamID: "t", TeamRole: "admin"}}
		},
	}
	for name, mutate := range cases {
		cfg := valid
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// auto_create is allowed once an allowed domain is present.
	cfg := valid
	cfg.AutoCreate = true
	cfg.AllowedEmailDomains = []string{"example.test"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("auto_create with a domain rejected: %v", err)
	}
}

// TestOIDCConfigSealBindsAAD: a config sealed for one provider does not open
// for another (#278).
func TestOIDCConfigSealBindsAAD(t *testing.T) {
	t.Parallel()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := OIDCProviderConfig{IssuerURL: "https://idp.example.test", ClientID: "c", ClientSecret: "s3cr3t"}
	sealed, err := SealOIDCConfig(kr, "provider-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "s3cr3t") {
		t.Fatalf("not a sealed envelope: %q", sealed)
	}
	if _, err := OpenOIDCConfig(kr, "provider-b", sealed); err == nil {
		t.Error("opened with the wrong AAD")
	}
}
