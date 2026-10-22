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
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
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
type Family string

const (
	FamilyIceberg   Family = "iceberg"
	FamilyMetastore Family = "metastore"
	FamilyDatabase  Family = "database"
	FamilyEngine    Family = "engine"
)

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
	ConnectionID pgtype.UUID
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

// State is a provider's support level.
type State string

const (
	StateSupported   State = "supported"
	StatePreview     State = "preview"
	StateUnsupported State = "unsupported"
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

var (
	// ErrUnsupported means the provider does not offer the operation.
	ErrUnsupported = errors.New("unsupported")
	// ErrUnknownKind means no provider is registered for the kind.
	ErrUnknownKind = errors.New("provider_unsupported")
	// ErrNotFound means the catalog, namespace or table does not exist.
	ErrNotFound = errors.New("not_found")
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
func (r *Registry) NewConfig(id pgtype.UUID, kind Kind) Config {
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
