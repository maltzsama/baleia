// Package synthetic declares the endpointless Trino catalog types Baleia can
// register without an external endpoint or credentials (ADR 0021 D.7): memory,
// Black Hole, Faker, JMX, TPC-H and TPC-DS. They are ordinary connections with
// an empty configuration schema, and their Trino catalogs need no properties
// beyond the connector name the binder already sends.
package synthetic

import (
	"context"

	"github.com/galileostudio/baleia/internal/connections"
)

// Provider is one endpointless catalog type.
type Provider struct {
	kind        connections.Kind
	label       string
	description string
	catalogType string
}

func (p *Provider) Kind() connections.Kind     { return p.kind }
func (p *Provider) Label() string              { return p.label }
func (p *Provider) Family() connections.Family { return connections.FamilyDatabase }

// Meta implements connections.Provider (ADR 0021 D.1). The type is Trino-only
// and has no endpoint, so it declares no configuration fields.
func (p *Provider) Meta() connections.TypeMeta {
	modes := []connections.AccessMode{connections.AccessModeTrino}
	return connections.TypeMeta{
		Description:       p.description,
		Category:          categoryOf(p.kind),
		AccessModes:       modes,
		TrinoCatalogTypes: []string{p.catalogType},
		Capabilities:      connections.CapabilitiesFor(modes, false),
	}
}

func (*Provider) Roles() []connections.Role         { return nil }
func (*Provider) EngineKind() string                { return "" }
func (*Provider) ConfigSchema() []connections.Field { return nil }

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	return connections.ValidateSchema(p.ConfigSchema(), cfg)
}

func (*Provider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, connections.ErrUnsupported
}

// Ping succeeds without a network call: a synthetic catalog has no endpoint.
func (*Provider) Ping(context.Context, connections.Config) error { return nil }

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateExperimental}
}

func categoryOf(kind connections.Kind) connections.Category {
	switch kind {
	case connections.KindFaker, connections.KindTPCH, connections.KindTPCDS:
		return connections.CategorySynthetic
	default:
		return connections.CategoryInternal
	}
}

// All returns the endpointless connection types in the ADR 0021 registry order.
func All() []connections.Provider {
	return []connections.Provider{
		&Provider{kind: connections.KindFaker, label: "Faker", description: "Synthetic Faker data, exposed through Trino.", catalogType: "faker"},
		&Provider{kind: connections.KindMemory, label: "Memory", description: "In-memory Trino catalog.", catalogType: "memory"},
		&Provider{kind: connections.KindBlackhole, label: "Black Hole", description: "Trino Black Hole catalog.", catalogType: "blackhole"},
		&Provider{kind: connections.KindJMX, label: "JMX", description: "Trino JMX monitoring catalog.", catalogType: "jmx"},
		&Provider{kind: connections.KindTPCH, label: "TPC-H", description: "TPC-H synthetic benchmark data.", catalogType: "tpch"},
		&Provider{kind: connections.KindTPCDS, label: "TPC-DS", description: "TPC-DS synthetic benchmark data.", catalogType: "tpcds"},
	}
}
