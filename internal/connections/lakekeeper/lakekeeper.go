// Package lakekeeper is the Lakekeeper Iceberg REST catalog provider: an
// Apache-licensed, Rust-native implementation of the Iceberg REST spec. Baleia
// reads its metadata through the standard Iceberg REST API and lists warehouses
// through Lakekeeper's management API.
//
// Spike against quay.io/lakekeeper/catalog:v0.14.0 (project #35):
//   - Management API, list warehouses: GET <url>/management/v1/warehouse →
//     {"warehouses":[{"id":…,"name":…}]}.
//   - Iceberg REST base path: <url>/catalog; GET /v1/config?warehouse=<name>
//     returns {"defaults":{"prefix":"<warehouse-id>"},…} — the warehouse *name*
//     is the addressing key, the prefix in the paths is the warehouse UUID.
//   - Auth: anonymous when authorization is off (dev); otherwise OIDC client
//     credentials via an external IdP (the token endpoint is not the Lakekeeper
//     URL), which the config models as auth_mode=oauth2.
//   - Vended credentials: Lakekeeper vends S3 credentials; the projection sets
//     iceberg.rest-catalog.vended-credentials-enabled=true.
package lakekeeper

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
)

const requestTimeout = 30 * time.Second

// Provider is the Lakekeeper provider.
type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindLakekeeper }
func (*Provider) Label() string              { return "Lakekeeper" }
func (*Provider) Family() connections.Family { return connections.FamilyIceberg }
func (*Provider) Roles() []connections.Role  { return []connections.Role{connections.RoleMetadata} }
func (*Provider) EngineKind() string         { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return append([]connections.Field{
		{Key: "url", Label: "Lakekeeper URL", Type: connections.FieldURL, Required: true, Help: "Server root, e.g. http://lakekeeper:8181 (without /catalog)"},
		{Key: "auth_mode", Label: "Authentication", Type: connections.FieldSelect, Options: []string{"none", "oauth2"}, Default: "none", Help: "none is for servers with authorization off"},
		{Key: "client_id", Label: "OAuth2 client ID", Type: connections.FieldText, Help: "Required with auth_mode=oauth2"},
		{Key: "client_secret", Label: "OAuth2 client secret", Type: connections.FieldSecret, Help: "Required with auth_mode=oauth2"},
		{Key: "token_endpoint", Label: "OAuth2 token endpoint", Type: connections.FieldURL, Help: "The IdP token endpoint (not the Lakekeeper URL); required with auth_mode=oauth2"},
		{Key: "scope", Label: "OAuth2 scope", Type: connections.FieldText},
	}, connections.S3StorageFields()...)
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	out.Values["url"] = strings.TrimRight(out.Values["url"], "/")
	if out.Value("auth_mode") == "oauth2" {
		var errs []connections.FieldError
		if out.Value("client_id") == "" {
			errs = append(errs, connections.FieldError{Key: "client_id", Message: "required with auth_mode=oauth2"})
		}
		if out.Secret("client_secret") == "" {
			errs = append(errs, connections.FieldError{Key: "client_secret", Message: "required with auth_mode=oauth2"})
		}
		if out.Value("token_endpoint") == "" {
			errs = append(errs, connections.FieldError{Key: "token_endpoint", Message: "required with auth_mode=oauth2"})
		}
		if len(errs) > 0 {
			return connections.Config{}, &connections.ValidationError{Fields: errs}
		}
	}
	return out, nil
}

// RESTURI is where Lakekeeper serves the Iceberg REST API.
func (*Provider) RESTURI(cfg connections.Config) string {
	return strings.TrimRight(cfg.Value("url"), "/") + "/catalog"
}

// auth is the OAuth2 client-credentials configuration, or anonymous.
func auth(cfg connections.Config) icebergrest.Auth {
	if cfg.Value("auth_mode") != "oauth2" {
		return icebergrest.Auth{}
	}
	return icebergrest.Auth{
		ClientID:     cfg.Value("client_id"),
		ClientSecret: cfg.Secret("client_secret"),
		TokenURI:     cfg.Value("token_endpoint"),
		Scope:        cfg.Value("scope"),
	}
}

func (p *Provider) client(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(p.RESTURI(cfg), auth(cfg), nil, p.dialer.HTTPClient(requestTimeout))
}

// management talks to the management API (base <url>/management), which is
// where warehouses are listed.
func (p *Provider) management(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(strings.TrimRight(cfg.Value("url"), "/")+"/management", auth(cfg), nil, p.dialer.HTTPClient(requestTimeout))
}

func (p *Provider) listWarehouses(cfg connections.Config) func(context.Context) ([]connections.CatalogRef, error) {
	mgmt := p.management(cfg)
	return func(ctx context.Context) ([]connections.CatalogRef, error) {
		var out struct {
			Warehouses []struct {
				Name string `json:"name"`
			} `json:"warehouses"`
		}
		if err := mgmt.Get(ctx, "list warehouses", "/v1/warehouse", nil, &out); err != nil {
			return nil, err
		}
		refs := make([]connections.CatalogRef, 0, len(out.Warehouses))
		for _, w := range out.Warehouses {
			refs = append(refs, connections.CatalogRef{Name: w.Name, Kind: "lakekeeper"})
		}
		return refs, nil
	}
}

// Ping lists warehouses through the management API: it proves reachability and
// that the credentials are accepted.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.listWarehouses(cfg)(ctx)
	return err
}

// OpenMetadata reads Lakekeeper warehouses; each warehouse is a catalog and its
// name is the addressing key.
func (p *Provider) OpenMetadata(_ context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	return p.client(cfg).ReaderWithCatalogs(p.listWarehouses(cfg), func(c string) string { return c }), nil
}
