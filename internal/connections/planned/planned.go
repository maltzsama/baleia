// Package planned declares connection kinds that are known but not supported
// yet, so the API can name them and explain why they cannot be created.
package planned

import (
	"context"

	"github.com/galileostudio/baleia/internal/connections"
)

// Provider is a kind whose support is tracked by an issue. Every operation
// returns connections.ErrUnsupported.
type Provider struct {
	kind   connections.Kind
	label  string
	reason string
}

// Doris is a federated engine planned after v1.
func Doris() *Provider {
	return &Provider{connections.KindDoris, "Apache Doris", "planned after v1 (#21)"}
}

// StarRocks is a federated engine planned after v1.
func StarRocks() *Provider {
	return &Provider{connections.KindStarRocks, "StarRocks", "planned after v1 (#30)"}
}

func (p *Provider) Kind() connections.Kind   { return p.kind }
func (p *Provider) Label() string            { return p.label }
func (*Provider) Family() connections.Family { return connections.FamilyEngine }
func (p *Provider) EngineKind() string       { return string(p.kind) }
func (p *Provider) Status() connections.Status {
	return connections.Status{State: connections.StateUnsupported, Reason: p.reason}
}
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleFederated}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "FE host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "MySQL protocol port", Type: connections.FieldInt, Default: "9030"},
		{Key: "username", Label: "Username", Type: connections.FieldText},
		{Key: "password", Label: "Password", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	return connections.ValidateSchema(p.ConfigSchema(), cfg)
}

func (*Provider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, connections.ErrUnsupported
}

func (*Provider) Ping(context.Context, connections.Config) error { return connections.ErrUnsupported }
