package auth

import (
	"encoding/json"
	"errors"
	"fmt"

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
