package trino

import "fmt"

// SecretPlaceholder is the textual reference the baleia-trino-catalog-store
// Java plugin recognizes and resolves at createCatalogProperties() time.
//
// Format: @baleia-secret[<catalog_name>:<property_key>]
//
// Why this syntax and not ${baleia-secret:...}:
// The Trino CLI (and several other tools in the ecosystem — bash, picocli,
// Spring) interpret ${...} as a variable expansion before the SQL reaches
// the plugin's createCatalogProperties. The Trino server routes CREATE
// CATALOG properties straight through, but any client that does its own
// ${...} parsing will eat our placeholder. @baleia-secret[...] sidesteps
// every standard expansion syntax. (See SecretResolver.java in the plugin
// repo for the matching regex.)
//
// The placeholder appears exclusively in the TEXT of the CREATE CATALOG
// DDL that Baleia emits. The trino_catalog_registry row stores the REAL
// secret — the plugin looks it up at boot by catalog_name and substitutes
// the placeholder with the real value before the connector is instantiated.
func SecretPlaceholder(catalogName, propertyKey string) string {
	return fmt.Sprintf("@baleia-secret[%s:%s]", catalogName, propertyKey)
}