package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/galileostudio/baleia/internal/auth"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
)

// Identity provider probe (#286). A probe never writes anything: it validates
// connectivity and credentials and returns a bounded check list. Raw errors,
// hosts and secrets are never exposed.
const (
	idpCheckOK      = IdentityProviderTestCheckStatus("ok")
	idpCheckFailed  = IdentityProviderTestCheckStatus("failed")
	idpCheckSkipped = IdentityProviderTestCheckStatus("skipped")

	idpProbeTimeout = 10 * time.Second
)

// probeIdentityProvider uses the injected probe when a test provides one.
func (h *Handlers) probeIdentityProvider(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult {
	if h.idpProbe != nil {
		return h.idpProbe(ctx, cfg, testUsername)
	}
	return defaultIdentityProviderProbe(ctx, cfg, testUsername)
}

func defaultIdentityProviderProbe(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult {
	switch {
	case cfg.oidc != nil:
		return probeOIDC(ctx, *cfg.oidc)
	case cfg.ldap != nil:
		return probeLDAP(ctx, *cfg.ldap, testUsername)
	default:
		return idpTestResult(idpCheck{name: "config", status: idpCheckFailed, message: "empty_configuration"})
	}
}

func probeLDAP(ctx context.Context, cfg identityldap.Config, testUsername string) IdentityProviderTestResult {
	checks := make([]idpCheck, 0, 3)
	for _, c := range identityldap.Probe(ctx, cfg, testUsername) {
		checks = append(checks, idpCheck{name: c.Name, status: IdentityProviderTestCheckStatus(c.Status), message: c.Message})
	}
	return idpTestResult(checks...)
}

type idpCheck struct {
	name    string
	status  IdentityProviderTestCheckStatus
	message string
}

func idpTestResult(checks ...idpCheck) IdentityProviderTestResult {
	out := IdentityProviderTestResult{Ok: true, Checks: make([]IdentityProviderTestCheck, 0, len(checks))}
	for _, c := range checks {
		item := IdentityProviderTestCheck{Name: c.name, Status: c.status}
		if c.message != "" {
			msg := c.message
			item.Message = &msg
		}
		if c.status == idpCheckFailed {
			out.Ok = false
		}
		out.Checks = append(out.Checks, item)
	}
	return out
}

// probeOIDC validates discovery, JWKS availability and, when advertised, a
// client-credentials grant. A provider that does not support client
// credentials skips that step instead of failing.
func probeOIDC(ctx context.Context, cfg auth.OIDCProviderConfig) IdentityProviderTestResult {
	client := &http.Client{Timeout: idpProbeTimeout}
	ctx = oidc.ClientContext(ctx, client)

	provider, err := auth.NewOIDCProvider(ctx, auth.OIDCConfig{
		IssuerURL:    cfg.IssuerURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	})
	if err != nil {
		return idpTestResult(idpCheck{name: "discovery", status: idpCheckFailed, message: "discovery_failed"})
	}
	checks := []idpCheck{{name: "discovery", status: idpCheckOK}}

	var discovery struct {
		JWKSURI       string   `json:"jwks_uri"`
		TokenEndpoint string   `json:"token_endpoint"`
		GrantTypes    []string `json:"grant_types_supported"`
	}
	_ = provider.Discovery(&discovery)

	switch {
	case discovery.JWKSURI == "":
		checks = append(checks, idpCheck{name: "jwks", status: idpCheckFailed, message: "jwks_uri_missing"})
	case probeJWKS(ctx, client, discovery.JWKSURI) != nil:
		checks = append(checks, idpCheck{name: "jwks", status: idpCheckFailed, message: "jwks_unreachable"})
	default:
		checks = append(checks, idpCheck{name: "jwks", status: idpCheckOK})
	}

	switch {
	case discovery.TokenEndpoint == "" || !slices.Contains(discovery.GrantTypes, "client_credentials"):
		checks = append(checks, idpCheck{name: "client_credentials", status: idpCheckSkipped, message: "grant_not_supported"})
	case probeClientCredentials(ctx, client, discovery.TokenEndpoint, cfg) != nil:
		checks = append(checks, idpCheck{name: "client_credentials", status: idpCheckFailed, message: "client_credentials_failed"})
	default:
		checks = append(checks, idpCheck{name: "client_credentials", status: idpCheckOK})
	}
	return idpTestResult(checks...)
}

func probeJWKS(ctx context.Context, client *http.Client, jwksURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errUnexpectedStatus
	}
	var body struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return err
	}
	if len(body.Keys) == 0 {
		return errEmptyJWKS
	}
	return nil
}

func probeClientCredentials(ctx context.Context, client *http.Client, tokenEndpoint string, cfg auth.OIDCProviderConfig) error {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errUnexpectedStatus
	}
	return nil
}

type probeError string

func (e probeError) Error() string { return string(e) }

const (
	errUnexpectedStatus probeError = "unexpected_status"
	errEmptyJWKS        probeError = "empty_jwks"
)
