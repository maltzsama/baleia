package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Browser binding and lifetime of one OIDC login attempt (#250).
const (
	OIDCCookieName = "baleia_oidc"
	OIDCCookiePath = "/api/v1/auth/oidc"
	OIDCAttemptTTL = 10 * time.Minute
)

// GeneratePKCE returns a 43-character RFC 7636 verifier and its S256
// challenge. The verifier stays server-side; only the challenge reaches the
// provider.
func GeneratePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	return verifier, PKCEChallenge(verifier), nil
}

// GenerateNonce returns a 32-byte nonce for the OIDC request (#277).
func GenerateNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallenge is the S256 challenge for a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// OIDCProvider is an experimental directory login provider (#55). Phase 4
// hardens it slice by slice; the admin UI lands later.
// OIDCClaimsConfig names the claims the provider publishes (#279). Empty
// fields use the standard names.
type OIDCClaimsConfig struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Groups  string `json:"groups"`
}

func (c OIDCClaimsConfig) withDefaults() OIDCClaimsConfig {
	if c.Subject == "" {
		c.Subject = "sub"
	}
	if c.Email == "" {
		c.Email = "email"
	}
	if c.Name == "" {
		c.Name = "name"
	}
	if c.Groups == "" {
		c.Groups = "groups"
	}
	return c
}

type OIDCProvider struct {
	provider *oidc.Provider
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	claims   OIDCClaimsConfig
}

type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Claims       OIDCClaimsConfig
}

type OIDCUserInfo struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

func NewOIDCProvider(ctx context.Context, cfg OIDCConfig) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}

	oauthConfig := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &OIDCProvider{
		provider: provider,
		config:   oauthConfig,
		verifier: verifier,
		claims:   cfg.Claims.withDefaults(),
	}, nil
}

// Discovery unmarshals the provider's discovery document into v. It is used
// by the admin probe to read jwks_uri, token_endpoint and the supported grants
// without exposing the raw document.
func (p *OIDCProvider) Discovery(v any) error {
	return p.provider.Claims(v)
}

// AuthURL builds the authorization URL with a mandatory S256 PKCE challenge
// and nonce.
func (p *OIDCProvider) AuthURL(state, challenge, nonce string) (string, error) {
	if state == "" || challenge == "" || nonce == "" {
		return "", errors.New("oidc: state, PKCE challenge and nonce are required")
	}
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oidc.Nonce(nonce)), nil
}

// Exchange redeems the code with the server-side PKCE verifier and checks the
// ID token nonce against the one stored with the attempt.
func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier, nonce string) (*OIDCUserInfo, error) {
	if verifier == "" || nonce == "" {
		return nil, errors.New("oidc: PKCE verifier and nonce are required")
	}
	token, err := p.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("missing id_token")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("token verify: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, errors.New("oidc: nonce mismatch")
	}

	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}

	subject := idToken.Subject
	if p.claims.Subject != "sub" {
		subject = claimString(raw, p.claims.Subject)
	}
	if subject == "" {
		return nil, errors.New("oidc: subject claim is empty")
	}
	email := claimString(raw, p.claims.Email)
	name := claimString(raw, p.claims.Name)
	groups := claimStrings(raw, p.claims.Groups)
	emailVerified := claimBool(raw, "email_verified")

	// Some providers only publish the display claims through userinfo.
	if email == "" || name == "" || len(groups) == 0 {
		if ui, err := p.provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil && ui.Subject == idToken.Subject {
			var ur map[string]any
			if err := ui.Claims(&ur); err == nil {
				if email == "" {
					email = claimString(ur, p.claims.Email)
				}
				if name == "" {
					name = claimString(ur, p.claims.Name)
				}
				if len(groups) == 0 {
					groups = claimStrings(ur, p.claims.Groups)
				}
				if !emailVerified {
					emailVerified = claimBool(ur, "email_verified")
				}
			}
		}
	}

	return &OIDCUserInfo{
		Subject:       subject,
		Email:         email,
		EmailVerified: emailVerified,
		Name:          name,
		Groups:        groups,
	}, nil
}

func claimString(raw map[string]any, name string) string {
	v, _ := raw[name].(string)
	return v
}

func claimBool(raw map[string]any, name string) bool {
	v, _ := raw[name].(bool)
	return v
}

func claimStrings(raw map[string]any, name string) []string {
	switch v := raw[name].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	}
	return nil
}
