package providers

import (
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

// topicCatalogs gives a provider a catalog schema, the way Kafka's topic set
// will (#1064).
type topicCatalogs struct{ connections.Provider }

func (topicCatalogs) CatalogSchema() []connections.Field {
	return []connections.Field{{Key: "topics", Label: "Topics", Type: connections.FieldText, Required: true}}
}

func (t topicCatalogs) ValidateCatalog(values map[string]string) (map[string]string, error) {
	return connections.ValidateCatalogSchema(t.CatalogSchema(), values)
}

func TestWriteCatalogFields(t *testing.T) {
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})
	pg, err := reg.Get(connections.KindPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}

	var plain strings.Builder
	writeCatalogFields(&plain, pg, "###")
	if plain.Len() != 0 {
		t.Errorf("a type without a catalog schema wrote %q", plain.String())
	}

	var withTopics strings.Builder
	writeCatalogFields(&withTopics, topicCatalogs{pg}, "###")
	if got := withTopics.String(); !strings.Contains(got, "### Catalog fields") || !strings.Contains(got, "`topics`") {
		t.Errorf("catalog fields section = %q", got)
	}
}
