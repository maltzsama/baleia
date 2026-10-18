package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/identity"
)

// OIDCProviderConfig is the secret part of an auth_providers row. It is sealed
// at rest with the keyring and bound to the provider id through AAD (#278).
// Provisioning defaults to deny (#251).
type OIDCProviderConfig struct {
	IssuerURL    string `json:"issuer_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`

	AutoCreate          bool                   `json:"auto_create"`
	AllowedEmailDomains []string               `json:"allowed_email_domains"`
	RoleMapping         []identity.RoleMapping `json:"role_mapping"`
	TeamMapping         []identity.TeamMapping `json:"team_mapping"`
	DefaultRole         string                 `json:"default_role"`
	Claims              OIDCClaimsConfig       `json:"claims"`
}

// Validate checks a provider config before it is sealed. It is the validator
// the admin API calls, so a malformed or insecure provider is rejected at the
// boundary instead of failing at the next login.
func (c OIDCProviderConfig) Validate() error {
	u, err := url.Parse(strings.TrimSpace(c.IssuerURL))
	if err != nil {
		return fmt.Errorf("oidc config: issuer_url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return errors.New("oidc config: issuer_url must be an absolute https URL")
	}
	if u.User != nil {
		return errors.New("oidc config: issuer_url must not contain credentials")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return errors.New("oidc config: client_id is required")
	}
	if c.ClientSecret == "" {
		return errors.New("oidc config: client_secret is required")
	}
	if c.AutoCreate && len(c.AllowedEmailDomains) == 0 {
		return errors.New("oidc config: auto_create requires allowed_email_domains")
	}
	for i, d := range c.AllowedEmailDomains {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("oidc config: allowed_email_domains %d is empty", i)
		}
	}
	if _, err := identity.MapIdentity(identity.MappingInput{
		RoleMapping: c.RoleMapping,
		TeamMapping: c.TeamMapping,
		DefaultRole: c.DefaultRole,
	}); err != nil {
		return fmt.Errorf("oidc config: %w", err)
	}
	return nil
}

// SealOIDCConfig encrypts the provider config. aad is the provider id.
func SealOIDCConfig(kr *crypto.Keyring, aad string, cfg OIDCProviderConfig) (string, error) {
	if kr == nil {
		return "", errors.New("oidc: keyring is required")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("oidc config: %w", err)
	}
	return kr.Seal(raw, aad)
}

// OpenOIDCConfig decrypts the provider config. It fails closed: a plaintext or
// corrupt value never falls back to JSON (#278).
func OpenOIDCConfig(kr *crypto.Keyring, aad, envelope string) (OIDCProviderConfig, error) {
	var cfg OIDCProviderConfig
	if kr == nil {
		return cfg, errors.New("oidc: keyring is required")
	}
	raw, err := kr.Open(envelope, aad)
	if err != nil {
		return cfg, fmt.Errorf("oidc config: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("oidc config: %w", err)
	}
	return cfg, nil
}
