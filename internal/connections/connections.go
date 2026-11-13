// Package connections is the provider contract for connections: credentials
// and an endpoint for one external system (docs/adr/0002). One provider per
// kind owns the kind's config schema, roles, validation, reachability check and
// metadata reader. Providers never know how a federated engine projects them;
// binders do (#80).
//
// Every provider dials through Dialer (docs/adr/0011).
package connections

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
)

// Kind is the connection type, stored in catalog_connections.type.
type Kind string

const (
	// Iceberg catalog managers and metastores: metadata and bindable, never engines.
	KindPolaris       Kind = "polaris"
	KindNessie        Kind = "nessie"
	KindIcebergREST   Kind = "iceberg"
	KindLakekeeper    Kind = "lakekeeper"
	KindUnity         Kind = "unity"
	KindHiveMetastore Kind = "hive"
	KindGlue          Kind = "glue"
	// Databases: metadata, direct engine, and bindable where a connector exists.
	KindPostgreSQL Kind = "postgresql"
	KindMySQL      Kind = "mysql"
	KindMariaDB    Kind = "mariadb"
	KindSQLServer  Kind = "sqlserver"
	KindOracle     Kind = "oracle"
	KindSQLite     Kind = "sqlite"
	KindClickHouse Kind = "clickhouse"
	// Engine services.
	KindFlightSQL Kind = "flightsql"
	KindTrino     Kind = "trino"
	KindDoris     Kind = "doris"
	KindStarRocks Kind = "starrocks"

	// Planned Trino catalog types (ADR 0021 D.9). Declared for the
	// interoperability inventory; not creatable until implemented.
	KindBigQuery      Kind = "bigquery"
	KindSnowflake     Kind = "snowflake"
	KindRedshift      Kind = "redshift"
	KindSingleStore   Kind = "singlestore"
	KindExasol        Kind = "exasol"
	KindCassandra     Kind = "cassandra"
	KindMongoDB       Kind = "mongodb"
	KindRedis         Kind = "redis"
	KindElasticsearch Kind = "elasticsearch"
	KindOpenSearch    Kind = "opensearch"
	KindDruid         Kind = "druid"
	KindPinot         Kind = "pinot"
	KindDuckDB        Kind = "duckdb"
	KindIgnite        Kind = "ignite"
	KindKafka         Kind = "kafka"
	KindLoki          Kind = "loki"
	KindPrometheus    Kind = "prometheus"
	KindGSheets       Kind = "gsheets"
	KindFaker         Kind = "faker"
	KindMemory        Kind = "memory"
	KindBlackhole     Kind = "blackhole"
	KindJMX           Kind = "jmx"
	KindTPCH          Kind = "tpch"
	KindTPCDS         Kind = "tpcds"
	KindDelta         Kind = "delta"
	KindHudi          Kind = "hudi"
	KindSystem        Kind = "system"
)

// Role is a way a connection can be used.
type Role string

const (
	RoleMetadata  Role = "metadata"      // implements MetadataReader
	RoleDirect    Role = "direct_engine" // an engine of EngineKind() can run on it
	RoleBindable  Role = "bindable"      // declared by binders (#80), never by providers
	RoleFederated Role = "federated_engine"
)

// Family groups kinds for forms and docs.
//
// Category is the presentation source of truth (ADR 0021 D.10); Family remains
// for API compatibility.
type Family string

const (
	FamilyIceberg   Family = "iceberg"
	FamilyMetastore Family = "metastore"
	FamilyDatabase  Family = "database"
	FamilyEngine    Family = "engine"
)

// AccessMode is a way a connection reaches its data (ADR 0021 D.2). A
// connection has exactly one when it represents a data source, and none when
// it is an engine-family resource.
type AccessMode string

const (
	// AccessModeDirect runs queries through a native query-engine adapter.
	AccessModeDirect AccessMode = "direct"
	// AccessModeFederated exposes the source through a separately registered
	// federated query engine (Trino today); the engine kind is independent of it.
	AccessModeFederated AccessMode = "federated"
)

// Category groups connection types for presentation only (ADR 0021 D.10); it
// never determines access mode, engine compatibility or catalog mapping.
type Category string

const (
	CategoryRelational     Category = "relational"
	CategoryAnalytical     Category = "analytical"
	CategoryCatalogManager Category = "catalog_manager"
	CategoryMetastore      Category = "metastore"
	CategoryLakehouse      Category = "lakehouse"
	CategoryCloudPlatform  Category = "cloud_platform"
	CategoryNoSQL          Category = "nosql"
	CategorySearch         Category = "search"
	CategoryAnalytics      Category = "analytics"
	CategoryStreaming      Category = "streaming"
	CategoryObservability  Category = "observability"
	CategoryExternal       Category = "external_service"
	CategorySynthetic      Category = "synthetic"
	CategoryInternal       Category = "internal_catalog"
	CategoryEngine         Category = "engine"
)

// CategoryMeta is a category's presentation metadata: its display name and a
// one-line description (ADR 0021 D.10 / CR-13). The order of Categories() is
// the UI order; it never determines access mode or compatibility.
type CategoryMeta struct {
	Category    Category
	Label       string
	Description string
}

// Categories returns every category in presentation order. It is the registry's
// single source of truth for category display names, descriptions and ordering
// (CR-13), so the frontend hard-codes none of them.
func Categories() []CategoryMeta {
	return []CategoryMeta{
		{CategoryRelational, "Relational databases", "SQL databases Baleia queries directly or exposes through Trino."},
		{CategoryAnalytical, "Analytical databases", "Columnar and analytical stores."},
		{CategoryCloudPlatform, "Cloud data platforms", "Managed cloud data warehouses."},
		{CategoryCatalogManager, "Catalog managers", "Iceberg REST catalogs and catalog managers."},
		{CategoryMetastore, "Metastores", "Hive Metastore and Glue catalog services."},
		{CategoryLakehouse, "Lakehouse table formats", "Delta Lake and Hudi table formats."},
		{CategoryNoSQL, "NoSQL and key-value", "Document, column-family and key-value stores."},
		{CategorySearch, "Search", "Search engines."},
		{CategoryAnalytics, "Analytics engines", "Real-time analytics engines."},
		{CategoryStreaming, "Streaming", "Streaming and event stores."},
		{CategoryObservability, "Observability", "Logs and metrics stores."},
		{CategoryExternal, "External services", "SaaS services and spreadsheets."},
		{CategorySynthetic, "Synthetic data", "Generated data sources."},
		{CategoryInternal, "Internal catalogs", "Engine-internal catalogs."},
		{CategoryEngine, "Engines", "Query engines Baleia runs SQL on."},
	}
}

// Capability is an operation a connection type may declare (ADR 0021 D.6).
type Capability string

const (
	CapabilityConnect        Capability = "connect"
	CapabilityTestConnection Capability = "test_connection"
	CapabilityListCatalogs   Capability = "list_catalogs"
	CapabilityListSchemas    Capability = "list_schemas"
	CapabilityListTables     Capability = "list_tables"
	CapabilityRead           Capability = "read"
	CapabilityInsert         Capability = "insert"
	CapabilityUpdate         Capability = "update"
	CapabilityDelete         Capability = "delete"
	CapabilityCreateTable    Capability = "create_table"
	CapabilityDropTable      Capability = "drop_table"
	CapabilityAlterTable     Capability = "alter_table"
	CapabilityMetadata       Capability = "metadata"
	CapabilityTransactions   Capability = "transactions"
	CapabilityViews          Capability = "views"
)

// CapabilityState is one capability's support state. Anything not validated
// stays CapabilityUnknown (ADR 0021 D.6).
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnknown     CapabilityState = "unknown"
	CapabilityUnsupported CapabilityState = "unsupported"
)

// Capabilities maps an access mode to its declared capabilities.
type Capabilities map[AccessMode]map[Capability]CapabilityState

// VersionRequirements are the validated engine and Trino version floors of a
// connection type. An empty value means no floor has been validated yet.
type VersionRequirements struct {
	MinEngineVersion string
	MinTrinoVersion  string
}

// TypeMeta is the static metadata a connection type declares (ADR 0021 D.1).
type TypeMeta struct {
	// Description is the user-facing explanation of the type.
	Description string
	// Category groups the type for presentation only (D.10).
	Category Category
	// AccessModes lists the supported data access paths (D.2).
	AccessModes []AccessMode
	// TrinoCatalogTypes is the internal set of Trino catalog implementations
	// required to expose the type (D.3). Empty when it is not Trino-bindable.
	TrinoCatalogTypes []string
	// Capabilities declares, per access mode, what the type can do (D.6).
	Capabilities Capabilities
	// VersionRequirements are the validated version floors.
	VersionRequirements VersionRequirements
}

// DefaultCapabilities is the capability set every implemented type can claim
// without per-connector validation: it validates and tests its configuration,
// and reads metadata when the type has a metadata reader. Everything else stays
// Unknown until validated (ADR 0021 D.6).
func DefaultCapabilities(metadata bool) map[Capability]CapabilityState {
	c := map[Capability]CapabilityState{
		CapabilityConnect:        CapabilitySupported,
		CapabilityTestConnection: CapabilitySupported,
	}
	if metadata {
		c[CapabilityMetadata] = CapabilitySupported
	}
	return c
}

// CapabilitiesFor builds the capability map of a type over its access modes.
func CapabilitiesFor(modes []AccessMode, metadata bool) Capabilities {
	out := make(Capabilities, len(modes))
	for _, m := range modes {
		out[m] = DefaultCapabilities(metadata)
	}
	return out
}

func allCapabilities() []Capability {
	return []Capability{
		CapabilityConnect, CapabilityTestConnection, CapabilityListCatalogs,
		CapabilityListSchemas, CapabilityListTables, CapabilityRead, CapabilityInsert,
		CapabilityUpdate, CapabilityDelete, CapabilityCreateTable, CapabilityDropTable,
		CapabilityAlterTable, CapabilityMetadata, CapabilityTransactions, CapabilityViews,
	}
}

// UnknownCapabilities declares every capability of every mode as unknown, which
// is what planned and not-yet-validated types must use (ADR 0021 D.6).
func UnknownCapabilities(modes []AccessMode) Capabilities {
	out := make(Capabilities, len(modes))
	for _, m := range modes {
		caps := make(map[Capability]CapabilityState, len(allCapabilities()))
		for _, c := range allCapabilities() {
			caps[c] = CapabilityUnknown
		}
		out[m] = caps
	}
	return out
}

// Field types used by ConfigSchema.
const (
	FieldText     = "text"
	FieldSecret   = "secret"
	FieldInt      = "int"
	FieldBool     = "bool"
	FieldSelect   = "select"
	FieldURL      = "url"
	FieldFilePath = "file_path"
)

// Field describes one config or secret value of a provider.
type Field struct {
	Key      string
	Label    string
	Type     string
	Required bool
	Default  string
	Options  []string
	Help     string
}

// Secret reports whether the field is stored sealed and never returned.
func (f Field) Secret() bool { return f.Type == FieldSecret }

// Config is a decrypted connection. Secrets are decrypted once per use and
// never logged.
type Config struct {
	ConnectionID db.UUID
	Kind         Kind
	Values       map[string]string
	Secrets      map[string]string
	// resolver resolves external secret references; a Config built by a
	// Registry carries the registry's (see Registry.NewConfig).
	resolver *SecretResolver
}

// Value returns a non-secret value, or "" when unset.
func (c Config) Value(key string) string { return c.Values[key] }

// Secret returns a secret value resolved for Baleia's own use: an external
// reference (@baleia-secret[env:…] or @baleia-secret[file:…]) is resolved with
// the Config's resolver; empty when it cannot be resolved.
func (c Config) Secret(key string) string {
	v := c.Secrets[key]
	if !IsSecretReference(v) {
		return v
	}
	// A Config built without a registry resolves env: references only.
	var r SecretResolver
	if c.resolver != nil {
		r = *c.resolver
	}
	resolved, err := r.Resolve(v)
	if err != nil {
		return ""
	}
	return resolved
}

// Reference returns a secret field's raw external reference, or "" when the
// field holds a literal secret. Binders project it verbatim, so the plugin
// persists the reference instead of the resolved value (#31).
func (c Config) Reference(key string) string {
	if v := c.Secrets[key]; IsSecretReference(v) {
		return v
	}
	return ""
}

// State is a provider's static implementation status (ADR 0021 CR-12). Runtime
// availability is a separate layer (D.5) and never changes this value.
type State string

const (
	StateSupported     State = "supported"
	StateExperimental  State = "experimental"
	StatePlanned       State = "planned"
	StateUnsupported   State = "unsupported"
	StateNotApplicable State = "not_applicable"
)

// Status is a provider's support level and, when not supported, why.
type Status struct {
	State  State
	Reason string
}

// Provider owns everything Baleia knows about one connection kind.
type Provider interface {
	Kind() Kind
	Label() string
	Family() Family
	// Meta is the static type metadata the registry is the single source of
	// truth for: access modes, Trino catalog mappings, capabilities, category,
	// description and version requirements (ADR 0021 CR-01).
	Meta() TypeMeta
	Roles() []Role
	// ConfigSchema lists the non-secret values and the secrets, for forms and
	// validation.
	ConfigSchema() []Field
	// Validate normalizes and validates a config; unknown keys are rejected.
	Validate(cfg Config) (Config, error)
	// EngineKind is the queryengine kind used for RoleDirect or
	// RoleFederated, or "" when the kind is never an engine.
	EngineKind() string
	// OpenMetadata returns a reader for RoleMetadata, else ErrUnsupported.
	OpenMetadata(ctx context.Context, cfg Config) (MetadataReader, error)
	// Ping checks reachability and credentials with the provider's own client.
	Ping(ctx context.Context, cfg Config) error
	Status() Status
}

// IcebergRESTEndpoint is implemented by providers that serve the Iceberg REST
// catalog API. Binders use it to point an engine's Iceberg connector at the
// provider.
type IcebergRESTEndpoint interface {
	// RESTURI is the base URI of the Iceberg REST API (the one /v1/config is
	// under).
	RESTURI(cfg Config) string
}

// UserIdentityKey is the config key of a connection whose catalog manager
// authorizes each Baleia user (#648).
const UserIdentityKey = "user_identity"

// UserIdentityField is the config field of a catalog manager that can
// authorize each Baleia user with their identity-provider token.
var UserIdentityField = Field{
	Key: UserIdentityKey, Label: "Per-user identity", Type: FieldBool, Default: "false",
	Help: "The catalog manager authorizes each Baleia user with the access token of the identity provider they signed in with; it must trust that provider. Users signed in without one, and background work, act as this connection's own credentials.",
}

// UserIdentity is implemented by catalog managers that can authorize each
// Baleia user (#648). Through Trino, the user's identity-provider token
// reaches the manager as the session's bearer token; a session without one
// carries the connection's own token, so the manager decides as that account.
type UserIdentity interface {
	// UserIdentity reports whether the connection is set to per-user identity.
	UserIdentity(cfg Config) bool
	// ServiceToken is a bearer token for the connection's own credentials,
	// valid for at least the next minute; "" when it has none.
	ServiceToken(ctx context.Context, cfg Config) (token string, expiry time.Time, err error)
}

// UserIdentityOn reads the per-user identity flag of a config.
func UserIdentityOn(cfg Config) bool { return cfg.Value(UserIdentityKey) == "true" }

var (
	// ErrUnsupported means the provider does not offer the operation.
	ErrUnsupported = errors.New("unsupported")
	// ErrUnknownKind means no provider is registered for the kind.
	ErrUnknownKind = errors.New("provider_unsupported")
	// ErrNotFound means the catalog, namespace or table does not exist.
	ErrNotFound = errors.New("not_found")
	// ErrTableUnsupported means the table exists but this provider's protocol
	// cannot read it (a plain Delta table behind Unity's Iceberg endpoint). A
	// walk skips it instead of failing the whole catalog.
	ErrTableUnsupported = errors.New("table_unsupported")
)

// Registry maps kinds to providers. It is built once at the wiring sites
// (docs/adr/0007) and read concurrently afterwards.
type Registry struct {
	providers map[Kind]Provider
	secrets   *SecretResolver
}

// NewRegistry registers the providers; a kind registered twice is an error.
func NewRegistry(providers ...Provider) (*Registry, error) {
	r := &Registry{providers: make(map[Kind]Provider, len(providers))}
	for _, p := range providers {
		if _, dup := r.providers[p.Kind()]; dup {
			return nil, fmt.Errorf("connections: provider %q registered twice", p.Kind())
		}
		r.providers[p.Kind()] = p
	}
	return r, nil
}

// WithSecrets returns a registry with the same providers whose Configs
// resolve external secret references with sr.
func (r *Registry) WithSecrets(sr SecretResolver) *Registry {
	return &Registry{providers: r.providers, secrets: &sr}
}

// NewConfig returns an empty Config of kind that resolves secret references
// with the registry's resolver.
func (r *Registry) NewConfig(id db.UUID, kind Kind) Config {
	return Config{ConnectionID: id, Kind: kind, Values: map[string]string{}, Secrets: map[string]string{}, resolver: r.secrets}
}

// Get returns the provider of a kind, or ErrUnknownKind.
func (r *Registry) Get(kind Kind) (Provider, error) {
	p, ok := r.providers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	return p, nil
}

// CatalogTypes returns the Trino catalog types a kind is exposed through,
// derived from the connection type registry (ADR 0021 D.3). Empty when the
// kind declares no Trino access mode or no mapping.
func (r *Registry) CatalogTypes(kind Kind) []string {
	p, err := r.Get(kind)
	if err != nil || !TrinoBindable(p) {
		return nil
	}
	return slices.Clone(p.Meta().TrinoCatalogTypes)
}

// TrinoBindable reports whether p is a creatable type that declares the Trino
// access mode with at least one catalog mapping (ADR 0021 D.3). It is the single
// predicate the registry, the Trino binder and the API share for
// connection-to-Trino compatibility. Planned and not-applicable types are not
// bindable because no connection of theirs can exist.
func TrinoBindable(p Provider) bool {
	if st := p.Status(); st.State != StateSupported && st.State != StateExperimental {
		return false
	}
	m := p.Meta()
	return slices.Contains(m.AccessModes, AccessModeFederated) && len(m.TrinoCatalogTypes) > 0
}

// HasTableHealth reports whether p is a creatable Iceberg catalog: its tables
// get health passes and Iceberg maintenance (#996).
func HasTableHealth(p Provider) bool {
	st := p.Status().State
	return p.Family() == FamilyIceberg && (st == StateSupported || st == StateExperimental)
}

// ReadOnlyKind is implemented by providers whose catalogs promise no writes:
// Baleia never runs maintenance on them, whatever the credentials allow.
type ReadOnlyKind interface {
	ReadOnlyKind() bool
}

// CatalogReadOnly reports whether a catalog of kind must be treated as
// read-only: its provider promises no writes, or the administrator asserted
// that the connection's credentials cannot modify data (#996).
func (r *Registry) CatalogReadOnly(kind Kind, credentialsReadOnly bool) bool {
	if credentialsReadOnly {
		return true
	}
	if r == nil {
		return false
	}
	p, err := r.Get(kind)
	if err != nil {
		return false
	}
	ro, ok := p.(ReadOnlyKind)
	return ok && ro.ReadOnlyKind()
}

// TableHealthKinds lists, ordered, the kinds whose catalogs get table health.
func (r *Registry) TableHealthKinds() []string {
	return r.kindsWhere(HasTableHealth)
}

// EngineTableHealth is implemented by metastore providers whose catalogs
// bound with the Iceberg connector get table health through the engine
// (#997): the provider lists the tables and tells their format, and the
// engine that binds the catalog reads each Iceberg table's metadata tables.
type EngineTableHealth interface {
	EngineTableHealth() bool
}

// HasEngineTableHealth reports whether p's Iceberg-connector catalogs get
// table health through the engine.
func HasEngineTableHealth(p Provider) bool {
	st := p.Status().State
	e, ok := p.(EngineTableHealth)
	return ok && e.EngineTableHealth() && (st == StateSupported || st == StateExperimental)
}

// EngineTableHealthKinds lists, ordered, the kinds whose Iceberg-connector
// catalogs get table health through the engine.
func (r *Registry) EngineTableHealthKinds() []string {
	return r.kindsWhere(HasEngineTableHealth)
}

// CatalogTableHealth reports whether a source catalog of kind, bound with
// connector, gets table health: every catalog of an Iceberg catalog manager,
// and a metastore's Iceberg-connector catalog when the provider supports it.
// An unknown kind does not.
func (r *Registry) CatalogTableHealth(kind Kind, connector string) bool {
	if r == nil {
		return false
	}
	p, err := r.Get(kind)
	if err != nil {
		return false
	}
	return HasTableHealth(p) || (connector == IcebergConnector && HasEngineTableHealth(p))
}

// IcebergConnector is the Trino catalog type that reads Iceberg tables.
const IcebergConnector = "iceberg"

func (r *Registry) kindsWhere(keep func(Provider) bool) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, p := range r.All() {
		if keep(p) {
			out = append(out, string(p.Kind()))
		}
	}
	return out
}

// All returns every provider ordered by kind.
func (r *Registry) All() []Provider {
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// HasRole reports whether the provider declares the role.
func HasRole(p Provider, role Role) bool {
	for _, r := range p.Roles() {
		if r == role {
			return true
		}
	}
	return false
}

// SecretKeys lists the secret field names of every registered provider (the
// audit trail redacts their values, #45).
func (r *Registry) SecretKeys() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range r.All() {
		for _, f := range p.ConfigSchema() {
			if f.Secret() && !seen[f.Key] {
				seen[f.Key] = true
				out = append(out, f.Key)
			}
		}
	}
	return out
}
