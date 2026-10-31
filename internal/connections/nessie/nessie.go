// Package nessie is the Project Nessie provider. Nessie serves the Iceberg
// REST API under <url>/iceberg; Baleia reads metadata through it.
package nessie

import (
	"context"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/iceberg"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
)

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindNessie }
func (*Provider) Label() string              { return "Project Nessie" }
func (*Provider) Family() connections.Family { return connections.FamilyIceberg }
func (*Provider) Roles() []connections.Role  { return []connections.Role{connections.RoleMetadata} }
func (*Provider) EngineKind() string         { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return append([]connections.Field{
		{Key: "url", Label: "Nessie URL", Type: connections.FieldURL, Required: true, Help: "Server root, e.g. http://nessie:19120 (without /iceberg)"},
		{Key: "warehouse", Label: "Warehouse", Type: connections.FieldText, Help: "Default: the server's default warehouse"},
		{Key: "token", Label: "Bearer token", Type: connections.FieldSecret},
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

// RESTURI is where Nessie serves the Iceberg REST API.
func (*Provider) RESTURI(cfg connections.Config) string {
	return strings.TrimRight(cfg.Value("url"), "/") + "/iceberg"
}

func (p *Provider) client(cfg connections.Config) *icebergrest.Client {
	return icebergrest.New(p.RESTURI(cfg), icebergrest.Auth{Token: cfg.Secret("token")}, nil, p.dialer.HTTPClient(30*time.Second))
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.client(cfg).Config(ctx, cfg.Value("warehouse"))
	return err
}

func (p *Provider) OpenMetadata(_ context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	return iceberg.SingleWarehouseReader(p.client(cfg), cfg.Value("warehouse")), nil
}
