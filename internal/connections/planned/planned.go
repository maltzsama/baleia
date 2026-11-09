// Package planned declares connection kinds that are known but not implemented
// yet, so the API can list them and explain why they cannot be created
// (ADR 0021 D.9). Their status is planned (or not_applicable for the reserved
// system catalog); every operation they do not implement returns
// connections.ErrUnsupported. The API rejects their creation, so a planned type
// is never inserted into catalog_connections.
package planned

import (
	"context"

	"github.com/galileostudio/baleia/internal/connections"
)

// Provider is a declared but non-creatable connection type.
type Provider struct {
	kind        connections.Kind
	label       string
	description string
	category    connections.Category
	family      connections.Family
	// catalogType is the internal Trino catalog implementation the type maps
	// to; empty for engine-family and reserved types.
	catalogType string
	// engineKind is the native query-engine kind for engine-family types.
	engineKind string
	roles      []connections.Role
	status     connections.Status
}

func (p *Provider) Kind() connections.Kind     { return p.kind }
func (p *Provider) Label() string              { return p.label }
func (p *Provider) Family() connections.Family { return p.family }

// Meta implements connections.Provider (ADR 0021 D.1). A planned Trino type
// declares the Trino access mode and its mapping, but every capability stays
// unknown until it is validated (D.6).
func (p *Provider) Meta() connections.TypeMeta {
	var modes []connections.AccessMode
	if p.catalogType != "" {
		modes = []connections.AccessMode{connections.AccessModeTrino}
	}
	meta := connections.TypeMeta{
		Description: p.description,
		Category:    p.category,
		AccessModes: modes,
	}
	if p.catalogType != "" {
		meta.TrinoCatalogTypes = []string{p.catalogType}
		meta.Capabilities = connections.UnknownCapabilities(modes)
	}
	return meta
}

func (p *Provider) Roles() []connections.Role       { return p.roles }
func (p *Provider) EngineKind() string              { return p.engineKind }
func (*Provider) ConfigSchema() []connections.Field { return nil }

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	return connections.ValidateSchema(p.ConfigSchema(), cfg)
}

func (*Provider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, connections.ErrUnsupported
}

func (*Provider) Ping(context.Context, connections.Config) error { return connections.ErrUnsupported }

func (p *Provider) Status() connections.Status { return p.status }

// familyFor keeps the deprecated Family field populated for API compatibility
// while Category is the source of truth (ADR 0021 D.10).
func familyFor(category connections.Category) connections.Family {
	switch category {
	case connections.CategoryCatalogManager, connections.CategoryLakehouse:
		return connections.FamilyIceberg
	case connections.CategoryMetastore:
		return connections.FamilyMetastore
	default:
		return connections.FamilyDatabase
	}
}

// trino declares a planned Trino-only type. A zero catalogType defaults to the
// kind itself.
func trino(kind connections.Kind, label, description string, category connections.Category, catalogType string) *Provider {
	if catalogType == "" {
		catalogType = string(kind)
	}
	return &Provider{
		kind: kind, label: label, description: description,
		category: category, family: familyFor(category), catalogType: catalogType,
		status: connections.Status{State: connections.StatePlanned},
	}
}

func engine(kind connections.Kind, label, description, reason string) *Provider {
	return &Provider{
		kind: kind, label: label, description: description,
		category: connections.CategoryEngine, family: connections.FamilyEngine,
		engineKind: string(kind), roles: []connections.Role{connections.RoleFederated},
		status: connections.Status{State: connections.StatePlanned, Reason: reason},
	}
}

// All returns every planned connection type in the ADR 0021 registry order.
func All() []connections.Provider {
	return []connections.Provider{
		// Cloud data platforms and relational databases.
		trino(connections.KindBigQuery, "BigQuery", "Google BigQuery data warehouse, exposed through Trino.", connections.CategoryCloudPlatform, ""),
		trino(connections.KindSnowflake, "Snowflake", "Snowflake data warehouse, exposed through Trino.", connections.CategoryCloudPlatform, ""),
		trino(connections.KindRedshift, "Amazon Redshift", "Amazon Redshift data warehouse, exposed through Trino.", connections.CategoryCloudPlatform, ""),
		trino(connections.KindSingleStore, "SingleStore", "SingleStore relational database, exposed through Trino.", connections.CategoryRelational, ""),
		trino(connections.KindExasol, "Exasol", "Exasol relational database, exposed through Trino.", connections.CategoryRelational, ""),
		// NoSQL and search.
		trino(connections.KindCassandra, "Apache Cassandra", "Apache Cassandra store, exposed through Trino.", connections.CategoryNoSQL, ""),
		trino(connections.KindMongoDB, "MongoDB", "MongoDB store, exposed through Trino.", connections.CategoryNoSQL, ""),
		trino(connections.KindRedis, "Redis", "Redis key-value store, exposed through Trino.", connections.CategoryNoSQL, ""),
		trino(connections.KindElasticsearch, "Elasticsearch", "Elasticsearch search store, exposed through Trino.", connections.CategorySearch, ""),
		trino(connections.KindOpenSearch, "OpenSearch", "OpenSearch search store, exposed through Trino.", connections.CategorySearch, ""),
		// Analytics and analytical databases.
		trino(connections.KindDruid, "Apache Druid", "Apache Druid analytics store, exposed through Trino.", connections.CategoryAnalytics, ""),
		trino(connections.KindPinot, "Apache Pinot", "Apache Pinot analytics store, exposed through Trino.", connections.CategoryAnalytics, ""),
		trino(connections.KindDuckDB, "DuckDB", "DuckDB analytical database, exposed through Trino.", connections.CategoryAnalytical, ""),
		trino(connections.KindIgnite, "Apache Ignite", "Apache Ignite distributed database, exposed through Trino.", connections.CategoryRelational, ""),
		// Streaming, observability and external services.
		trino(connections.KindKafka, "Apache Kafka", "Apache Kafka streaming store, exposed through Trino.", connections.CategoryStreaming, ""),
		trino(connections.KindLoki, "Grafana Loki", "Grafana Loki logs, exposed through Trino.", connections.CategoryObservability, ""),
		trino(connections.KindPrometheus, "Prometheus", "Prometheus metrics, exposed through Trino.", connections.CategoryObservability, ""),
		trino(connections.KindGSheets, "Google Sheets", "Google Sheets spreadsheets, exposed through Trino.", connections.CategoryExternal, ""),
		// Synthetic and internal catalogs.
		trino(connections.KindFaker, "Faker", "Synthetic Faker data, exposed through Trino.", connections.CategorySynthetic, ""),
		trino(connections.KindMemory, "Memory", "In-memory Trino catalog.", connections.CategoryInternal, ""),
		trino(connections.KindBlackhole, "Black Hole", "Trino Black Hole catalog.", connections.CategoryInternal, ""),
		trino(connections.KindJMX, "JMX", "Trino JMX monitoring catalog.", connections.CategoryInternal, ""),
		trino(connections.KindTPCH, "TPC-H", "TPC-H synthetic benchmark data.", connections.CategorySynthetic, ""),
		trino(connections.KindTPCDS, "TPC-DS", "TPC-DS synthetic benchmark data.", connections.CategorySynthetic, ""),
		// Lakehouse table formats.
		trino(connections.KindDelta, "Delta Lake", "Delta Lake tables, exposed through Trino.", connections.CategoryLakehouse, "delta_lake"),
		trino(connections.KindHudi, "Apache Hudi", "Apache Hudi tables, exposed through Trino.", connections.CategoryLakehouse, "hudi"),
		// Reserved system catalog: represented, never user-created (ADR 0021
		// D.7 proposal).
		&Provider{
			kind: connections.KindSystem, label: "System",
			description: "Trino system catalog, reserved and not user-creatable.",
			category:    connections.CategoryInternal, family: connections.FamilyDatabase,
			status: connections.Status{State: connections.StateNotApplicable, Reason: "reserved system catalog; not user-creatable"},
		},
		// Engine-family types planned after v1.
		engine(connections.KindDoris, "Apache Doris", "Apache Doris federated query engine.", "planned after v1 (#21)"),
		engine(connections.KindStarRocks, "StarRocks", "StarRocks federated query engine.", "planned after v1 (#30)"),
	}
}
