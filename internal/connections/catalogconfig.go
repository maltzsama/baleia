package connections

import "fmt"

// CatalogConfigurer is implemented by providers whose catalogs carry their own
// configuration, next to the connection's (the "registration options" of ADR
// 0021 and ADR 0029): what one logical catalog of the connection exposes, such
// as the topic set of a Kafka catalog. Binders read the validated values from
// BindingSpec.Config.
//
// Catalog values are stored in plain text with the catalog, so a catalog schema
// never declares a secret field; NewRegistry refuses one that does.
type CatalogConfigurer interface {
	// CatalogSchema lists the values one catalog of the connection takes.
	CatalogSchema() []Field
	// ValidateCatalog normalizes and validates a catalog's values; unknown keys
	// are rejected. Implementations call ValidateCatalogSchema first.
	ValidateCatalog(values map[string]string) (map[string]string, error)
}

// CatalogSchemaOf returns the catalog schema of p, or nil when its catalogs
// take no configuration.
func CatalogSchemaOf(p Provider) []Field {
	c, ok := p.(CatalogConfigurer)
	if !ok {
		return nil
	}
	return c.CatalogSchema()
}

// ValidateCatalogConfig validates the catalog values of a catalog of p. A
// provider without a catalog schema accepts no values.
func ValidateCatalogConfig(p Provider, values map[string]string) (map[string]string, error) {
	c, ok := p.(CatalogConfigurer)
	if !ok {
		if len(values) == 0 {
			return map[string]string{}, nil
		}
		errs := make([]FieldError, 0, len(values))
		for _, k := range sortedKeys(values) {
			errs = append(errs, FieldError{Key: k, Message: "unknown field"})
		}
		return nil, &ValidationError{Fields: errs}
	}
	return c.ValidateCatalog(values)
}

// ValidateCatalogSchema checks catalog values against a catalog schema and
// returns a normalized copy: values trimmed, defaults applied, unknown keys
// rejected.
func ValidateCatalogSchema(schema []Field, values map[string]string) (map[string]string, error) {
	cfg, err := ValidateSchema(schema, Config{Values: values})
	if err != nil {
		return nil, err
	}
	return cfg.Values, nil
}

// checkCatalogSchema refuses a catalog schema that declares a secret field.
func checkCatalogSchema(p Provider) error {
	for _, f := range CatalogSchemaOf(p) {
		if f.Secret() {
			return fmt.Errorf("connections: provider %q declares the secret catalog field %q", p.Kind(), f.Key)
		}
	}
	return nil
}
