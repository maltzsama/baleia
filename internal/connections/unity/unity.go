// Package unity is the Unity Catalog provider. Unity is not Iceberg-REST-first:
// it exposes its own REST API for browsing and an Iceberg REST endpoint for
// reading tables (UniForm/Iceberg). Baleia browses catalogs/schemas/tables
// through the Unity API and reads table metadata through the Iceberg REST
// endpoint; bindings of this kind are read-oriented.
//
// Spike against unitycatalog/unitycatalog:v0.7.0 (project #32):
//   - Browsing API: GET <url>/api/2.1/unity-catalog/catalogs →
//     {"catalogs":[{"name":…}],"next_page_token":…}; schemas/tables under
//     /api/2.1/unity-catalog/schemas?catalog_name=… and /tables?catalog_name=&schema_name=.
//   - Iceberg REST base path (OSS): <url>/api/2.1/unity-catalog/iceberg;
//     GET /v1/config?warehouse=<catalog> returns {"overrides":{"prefix":"catalogs/<catalog>"}}.
//     Databricks-hosted Unity differs (no /v1/config warehouse path there); only the
//     OSS path was spiked. The base path is overridable for Databricks.
//   - Auth: OSS is anonymous; a bearer token (Databricks PAT) is supported.
//   - Credential vending: the Iceberg REST endpoint lists
//     GET /v1/{prefix}/namespaces/{namespace}/tables/{table}/credentials.
//   - Reading: the OSS image lists namespaces/tables through the Iceberg REST
//     endpoint, but LoadTable of its seeded table answers 500 ("Could not read
//     this table") because the demo storage is not reachable from the server;
//     a deployment with readable storage returns metadata. Only reading is
//     promised.
//
// Trino path A (chosen): the iceberg connector with
// iceberg.catalog.type=rest, uri=<Iceberg REST endpoint>, warehouse=<catalog>.
// Writes are not promised: the binder marks this kind read-only.
package unity

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
)

const (
	requestTimeout = 30 * time.Second
	// defaultIcebergPath is where UC OSS serves the Iceberg REST API.
	defaultIcebergPath = "/api/2.1/unity-catalog/iceberg"
	// catalogsPath lists catalogs through the Unity REST API.
	catalogsPath = "/api/2.1/unity-catalog/catalogs"
)

// Provider is the Unity Catalog provider.
type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindUnity }
func (*Provider) Label() string              { return "Unity Catalog" }
func (*Provider) Family() connections.Family { return connections.FamilyIceberg }
func (*Provider) Roles() []connections.Role  { return []connections.Role{connections.RoleMetadata} }
func (*Provider) EngineKind() string         { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "url", Label: "Unity URL", Type: connections.FieldURL, Required: true, Help: "Server root, e.g. http://unity:8080 or a Databricks workspace URL"},
		{Key: "token", Label: "Bearer token", Type: connections.FieldSecret, Help: "OSS token or a Databricks personal access token"},
		{Key: "iceberg_path", Label: "Iceberg REST path", Type: connections.FieldText, Default: defaultIcebergPath, Help: "Override for Databricks: /api/2.1/unity-catalog/iceberg-rest"},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	out.Values["url"] = strings.TrimRight(out.Values["url"], "/")
	pth := out.Values["iceberg_path"]
	if pth == "" {
		pth = defaultIcebergPath
	}
	if !strings.HasPrefix(pth, "/") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{
			{Key: "iceberg_path", Message: "must start with /"},
		}}
	}
	out.Values["iceberg_path"] = strings.TrimRight(pth, "/")
	return out, nil
}

// RESTURI is where Unity serves the Iceberg REST API.
func (*Provider) RESTURI(cfg connections.Config) string {
	pth := cfg.Value("iceberg_path")
	if pth == "" {
		pth = defaultIcebergPath
	}
	return strings.TrimRight(cfg.Value("url"), "/") + pth
}

func (p *Provider) client(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(p.RESTURI(cfg), icebergrest.Auth{Token: cfg.Secret("token")}, nil, p.dialer.HTTPClient(requestTimeout))
}

// browsing talks to the Unity REST API (base <url>), which lists catalogs.
func (p *Provider) browsing(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(strings.TrimRight(cfg.Value("url"), "/"), icebergrest.Auth{Token: cfg.Secret("token")}, nil, p.dialer.HTTPClient(requestTimeout))
}

func (p *Provider) listCatalogs(cfg connections.Config) func(context.Context) ([]connections.CatalogRef, error) {
	b := p.browsing(cfg)
	return func(ctx context.Context) ([]connections.CatalogRef, error) {
		var out struct {
			Catalogs []struct {
				Name string `json:"name"`
			} `json:"catalogs"`
		}
		if err := b.Get(ctx, "list catalogs", catalogsPath, nil, &out); err != nil {
			return nil, err
		}
		refs := make([]connections.CatalogRef, 0, len(out.Catalogs))
		for _, c := range out.Catalogs {
			refs = append(refs, connections.CatalogRef{Name: c.Name, Kind: "unity"})
		}
		return refs, nil
	}
}

// Ping lists catalogs through the Unity API: it proves reachability and that
// the token is accepted.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.listCatalogs(cfg)(ctx)
	return err
}

// OpenMetadata reads Unity catalogs; each catalog is a warehouse addressed by
// name.
func (p *Provider) OpenMetadata(_ context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	return p.client(cfg).ReaderWithCatalogs(p.listCatalogs(cfg), func(c string) string { return c }), nil
}
