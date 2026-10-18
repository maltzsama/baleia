package ldap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/identity"
)

func testKeyring(t *testing.T, secret string) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring(secret, "")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return kr
}

func validConfig() Config {
	return Config{
		URL:          "ldaps://ldap.example.org:636",
		BindDN:       "cn=svc,ou=services,dc=example,dc=org",
		BindPassword: "service-secret",
		BaseDN:       "ou=people,dc=example,dc=org",
		UserFilter:   "(uid={{username}})",
	}.Normalize()
}

func TestLDAPConfig_PlainLDAPRejected(t *testing.T) {
	cfg := validConfig()
	cfg.URL = "ldap://ldap.example.org:389"
	if err := cfg.Validate(); err == nil {
		t.Fatal("plaintext ldap:// was accepted without start_tls")
	}
}

func TestLDAPConfig_StartTLSAllowed(t *testing.T) {
	cfg := validConfig()
	cfg.URL = "ldap://ldap.example.org:389"
	cfg.StartTLS = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ldap:// with start_tls rejected: %v", err)
	}
}

func TestLDAPConfig_CAPem(t *testing.T) {
	cfg := validConfig()
	cfg.CAPem = testCACertPEM(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid ca_pem rejected: %v", err)
	}
	if _, err := parseCAs(cfg.CAPem); err != nil {
		t.Fatalf("parseCAs: %v", err)
	}

	cfg.CAPem = "not a certificate"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid ca_pem accepted")
	}
}

// testCACertPEM returns a throwaway self-signed CA in PEM form.
func testCACertPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "baleia-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestLDAPConfig_RequiredFields(t *testing.T) {
	mutations := map[string]func(*Config){
		"bind_dn":       func(c *Config) { c.BindDN = "" },
		"bind_password": func(c *Config) { c.BindPassword = "" },
		"base_dn":       func(c *Config) { c.BaseDN = "" },
		"user_filter":   func(c *Config) { c.UserFilter = "" },
		"placeholder":   func(c *Config) { c.UserFilter = "(uid=admin)" },
		"timeout_low":   func(c *Config) { c.TimeoutSeconds = 0 },
		"timeout_high":  func(c *Config) { c.TimeoutSeconds = MaxTimeoutSeconds + 1 },
		"role":          func(c *Config) { c.DefaultRole = "superuser" },
		"team_id": func(c *Config) {
			c.TeamMapping = []identity.TeamMapping{{Group: "g", TeamRole: "member"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("config with invalid %s was accepted", name)
			}
		})
	}
}

func TestLDAPConfig_StrictDecode(t *testing.T) {
	raw := []byte(`{
		"url": "ldaps://ldap.example.org:636",
		"bind_dn": "cn=svc,ou=services,dc=example,dc=org",
		"bind_password": "service-secret",
		"base_dn": "ou=people,dc=example,dc=org",
		"user_filter": "(uid={{username}})",
		"start_tlss": true
	}`)
	if _, err := Decode(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode accepted an unknown key: %v", err)
	}
}

func TestLDAPConfig_SecretsEncrypted(t *testing.T) {
	kr := testKeyring(t, strings.Repeat("k", 32))
	cfg := validConfig()

	envelope, err := SealConfig(kr, "provider-1", cfg)
	if err != nil {
		t.Fatalf("SealConfig: %v", err)
	}
	if !strings.HasPrefix(envelope, "v1:") {
		t.Fatalf("envelope is not versioned: %q", envelope)
	}
	if strings.Contains(envelope, cfg.BindPassword) {
		t.Fatal("sealed envelope contains the bind password")
	}

	opened, err := OpenConfig(kr, "provider-1", envelope)
	if err != nil {
		t.Fatalf("OpenConfig: %v", err)
	}
	if opened.BindPassword != cfg.BindPassword {
		t.Fatalf("bind password round-trip = %q", opened.BindPassword)
	}

	if _, err := OpenConfig(kr, "provider-2", envelope); err == nil {
		t.Fatal("config opened with a different provider id")
	}
	if _, err := OpenConfig(kr, "provider-1", `{"bind_password":"plaintext"}`); err == nil {
		t.Fatal("plaintext config opened; must fail closed")
	}
}
