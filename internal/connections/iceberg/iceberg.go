// Package iceberg is the generic Iceberg REST catalog provider. The URL is the
// REST base URI, used verbatim.
package iceberg

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
)

// DefaultCatalog names the single catalog when no warehouse is configured.
const DefaultCatalog = "default"

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindIcebergREST }
func (*Provider) Label() string              { return "Iceberg REST catalog" }
func (*Provider) Family() connections.Family { return connections.FamilyIceberg }
func (*Provider) Roles() []connections.Role  { return []connections.Role{connections.RoleMetadata} }
func (*Provider) EngineKind() string         { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "url", Label: "REST URI", Type: connections.FieldURL, Required: true, Help: "Base URI of the Iceberg REST API (the one /v1/config is under)"},
		{Key: "warehouse", Label: "Warehouse", Type: connections.FieldText},
		{Key: "token", Label: "Bearer token", Type: connections.FieldSecret},
		{Key: "client_id", Label: "OAuth2 client ID", Type: connections.FieldText},
		{Key: "client_secret", Label: "OAuth2 client secret", Type: connections.FieldSecret},
		{Key: "oauth2_server_uri", Label: "OAuth2 token endpoint", Type: connections.FieldURL, Help: "Default: <REST URI>/v1/oauth/tokens"},
		{Key: "scope", Label: "OAuth2 scope", Type: connections.FieldText},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	out.Values["url"] = strings.TrimRight(out.Values["url"], "/")
	if (out.Values["client_id"] == "") != (out.Secrets["client_secret"] == "") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{
			{Key: "client_secret", Message: "client_id and client_secret go together"},
		}}
	}
	return out, nil
}

// RESTURI is the configured URL, verbatim.
func (*Provider) RESTURI(cfg connections.Config) string {
	return strings.TrimRight(cfg.Value("url"), "/")
}

func (p *Provider) client(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(p.RESTURI(cfg), icebergrest.Auth{
		Token:        cfg.Secret("token"),
		ClientID:     cfg.Value("client_id"),
		ClientSecret: cfg.Secret("client_secret"),
		TokenURI:     cfg.Value("oauth2_server_uri"),
		Scope:        cfg.Value("scope"),
	}, nil, p.dialer.HTTPClient(30*time.Second))
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.client(cfg).Config(ctx, cfg.Value("warehouse"))
	return err
}

func (p *Provider) OpenMetadata(_ context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	return SingleWarehouseReader(p.client(cfg), cfg.Value("warehouse")), nil
}

// SingleWarehouseReader exposes one warehouse as one catalog, named after the
// warehouse, or DefaultCatalog when the server default is used.
func SingleWarehouseReader(c *icebergrest.Client, warehouse string) connections.MetadataReader {
	name := warehouse
	if name == "" {
		name = DefaultCatalog
	}
	return c.Reader([]connections.CatalogRef{{Name: name, Kind: "iceberg"}}, func(string) string { return warehouse })
}
