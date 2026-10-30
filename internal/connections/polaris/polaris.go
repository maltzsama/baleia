// Package polaris is the Apache Polaris provider: an Iceberg REST catalog
// under <url>/api/catalog with OAuth2 client credentials and a management API
// that lists catalogs.
package polaris

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
)

// Scope is the OAuth2 scope Baleia requests: every principal role of the
// connection's principal.
const Scope = "PRINCIPAL_ROLE:ALL"

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindPolaris }
func (*Provider) Label() string              { return "Apache Polaris" }
func (*Provider) Family() connections.Family { return connections.FamilyIceberg }
func (*Provider) Roles() []connections.Role  { return []connections.Role{connections.RoleMetadata} }
func (*Provider) EngineKind() string         { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return append([]connections.Field{
		{Key: "url", Label: "Polaris URL", Type: connections.FieldURL, Required: true, Help: "Server root, e.g. http://polaris:8181 (without /api/catalog)"},
		{Key: "realm", Label: "Realm", Type: connections.FieldText, Default: "POLARIS"},
		{Key: "client_id", Label: "Client ID", Type: connections.FieldText, Required: true},
		{Key: "client_secret", Label: "Client secret", Type: connections.FieldSecret, Required: true},
	}, connections.S3StorageFields()...)
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	out.Values["url"] = strings.TrimRight(out.Values["url"], "/")
	return out, nil
}

// RESTURI is where Polaris serves the Iceberg REST API.
func (*Provider) RESTURI(cfg connections.Config) string {
	return strings.TrimRight(cfg.Value("url"), "/") + "/api/catalog"
}

func (p *Provider) client(cfg connections.Config, base string) *icebergrest.Client {
	return icebergrest.New(base, icebergrest.Auth{
		ClientID:     cfg.Value("client_id"),
		ClientSecret: cfg.Secret("client_secret"),
		TokenURI:     p.RESTURI(cfg) + "/v1/oauth/tokens",
		Scope:        Scope,
	}, map[string]string{"Polaris-Realm": realm(cfg)}, p.dialer.HTTPClient(30*time.Second))
}

func realm(cfg connections.Config) string {
	if r := cfg.Value("realm"); r != "" {
		return r
	}
	return "POLARIS"
}

func (p *Provider) management(cfg connections.Config) *icebergrest.Client {
	return p.client(cfg, strings.TrimRight(cfg.Value("url"), "/")+"/api/management")
}

func (p *Provider) listCatalogs(cfg connections.Config) func(context.Context) ([]connections.CatalogRef, error) {
	mgmt := p.management(cfg)
	return func(ctx context.Context) ([]connections.CatalogRef, error) {
		var out struct {
			Catalogs []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"catalogs"`
		}
		if err := mgmt.Get(ctx, "list catalogs", "/v1/catalogs", nil, &out); err != nil {
			return nil, err
		}
		refs := make([]connections.CatalogRef, 0, len(out.Catalogs))
		for _, c := range out.Catalogs {
			refs = append(refs, connections.CatalogRef{Name: c.Name, Kind: strings.ToLower(c.Type)})
		}
		return refs, nil
	}
}

// Ping lists catalogs through the management API: it proves reachability and
// that the credentials are accepted.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.listCatalogs(cfg)(ctx)
	return err
}

// OpenMetadata reads Polaris catalogs; each Polaris catalog is a warehouse.
func (p *Provider) OpenMetadata(_ context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	return p.client(cfg, p.RESTURI(cfg)).ReaderWithCatalogs(p.listCatalogs(cfg), func(c string) string { return c }), nil
}
