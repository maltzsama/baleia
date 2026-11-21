package connections

import (
	"context"
	"maps"
	"slices"
	"testing"
)

// plainProvider is a provider whose catalogs take no configuration.
type plainProvider struct{ kind Kind }

func (p plainProvider) Kind() Kind                        { return p.kind }
func (plainProvider) Label() string                       { return "Plain" }
func (plainProvider) Family() Family                      { return FamilyDatabase }
func (plainProvider) Meta() TypeMeta                      { return TypeMeta{} }
func (plainProvider) Roles() []Role                       { return nil }
func (plainProvider) ConfigSchema() []Field               { return nil }
func (plainProvider) Validate(cfg Config) (Config, error) { return cfg, nil }
func (plainProvider) EngineKind() string                  { return "" }
func (plainProvider) Ping(context.Context, Config) error  { return nil }
func (plainProvider) Status() Status                      { return Status{State: StateSupported} }
func (plainProvider) OpenMetadata(context.Context, Config) (MetadataReader, error) {
	return nil, ErrUnsupported
}

// catalogProvider is a provider whose catalogs take the given schema.
type catalogProvider struct {
	plainProvider
	schema []Field
}

func (p catalogProvider) CatalogSchema() []Field { return p.schema }

func (p catalogProvider) ValidateCatalog(values map[string]string) (map[string]string, error) {
	return ValidateCatalogSchema(p.schema, values)
}

var testCatalogSchema = []Field{
	{Key: "topics", Type: FieldText, Required: true},
	{Key: "schema", Type: FieldText, Default: "default"},
	{Key: "internal", Type: FieldBool, Default: "true"},
}

func TestCatalogSchemaOf(t *testing.T) {
	t.Parallel()
	if got := CatalogSchemaOf(plainProvider{kind: "plain"}); got != nil {
		t.Errorf("plain provider schema = %v, want nil", got)
	}
	p := catalogProvider{plainProvider: plainProvider{kind: "topics"}, schema: testCatalogSchema}
	if got := CatalogSchemaOf(p); len(got) != len(testCatalogSchema) {
		t.Errorf("schema = %v, want %v", got, testCatalogSchema)
	}
}

func TestValidateCatalogConfig(t *testing.T) {
	t.Parallel()
	configurable := catalogProvider{plainProvider: plainProvider{kind: "topics"}, schema: testCatalogSchema}
	tests := []struct {
		name       string
		provider   Provider
		values     map[string]string
		expected   map[string]string
		wantFields []string
	}{
		{
			name:     "no schema and no values",
			provider: plainProvider{kind: "plain"},
			expected: map[string]string{},
		},
		{
			name:       "no schema refuses every value",
			provider:   plainProvider{kind: "plain"},
			values:     map[string]string{"b": "1", "a": "2"},
			wantFields: []string{"a", "b"},
		},
		{
			name:     "schema normalizes and applies defaults",
			provider: configurable,
			values:   map[string]string{"topics": " orders,payments "},
			expected: map[string]string{"topics": "orders,payments", "schema": "default", "internal": "true"},
		},
		{
			name:       "schema refuses unknown and missing values",
			provider:   configurable,
			values:     map[string]string{"extra": "x", "internal": "maybe"},
			wantFields: []string{"extra", "topics", "internal"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateCatalogConfig(tt.provider, tt.values)
			if tt.wantFields != nil {
				if keys := fieldKeys(err); !slices.Equal(keys, tt.wantFields) {
					t.Fatalf("invalid fields = %v (err %v), want %v", keys, err, tt.wantFields)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.expected) {
				t.Errorf("values = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewRegistryRefusesSecretCatalogFields(t *testing.T) {
	t.Parallel()
	p := catalogProvider{
		plainProvider: plainProvider{kind: "leaky"},
		schema:        []Field{{Key: "token", Type: FieldSecret}},
	}
	if _, err := NewRegistry(p); err == nil {
		t.Fatal("NewRegistry accepted a secret catalog field")
	}
	ok := catalogProvider{plainProvider: plainProvider{kind: "topics"}, schema: testCatalogSchema}
	if _, err := NewRegistry(ok, plainProvider{kind: "plain"}); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
}
