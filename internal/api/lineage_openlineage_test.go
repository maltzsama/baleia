package api

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestLineageRunEventUsesGrantedTables(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ws := db.UUID{Bytes: f.ws, Valid: true}
	content, _, err := f.h.lineageContent(context.Background(), ws, f.ownerUUID)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := f.h.catalogGrants().ListWorkspaceCatalogGrants(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	catalogs := map[string]string{}
	for _, grant := range grants {
		catalogs[idString(grant.CatalogID)] = grant.CatalogName
	}
	checked := 0
	schemaBytes, err := os.ReadFile("../openlineage/testdata/OpenLineage-1-0-5.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatal(err)
	}
	schema.Ref = "#/$defs/RunEvent"
	// The publisher emits no facets. External facet schemas are never evaluated
	// for these payloads; the loader avoids network access in the test.
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{Loader: func(_ *url.URL) (*jsonschema.Schema, error) { return &jsonschema.Schema{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range content {
		event, ok, err := f.h.lineageRunEvent(context.Background(), ws, "example", "01d43de5-c603-4b46-8506-a054ac387434", item, catalogs)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("no event for %s", item.node.Id)
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(payload); err != nil {
			t.Fatalf("event violates OpenLineage 1.0.5 schema: %v", err)
		}
		if len(event.Inputs) != 1 || event.Inputs[0].Name != "lake.sales.orders" || len(event.Outputs) != 1 || !strings.HasPrefix(event.Outputs[0].Name, "lake.sales.baleia_") {
			t.Fatalf("unexpected event: %+v", event)
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("got %d events, want 3", checked)
	}
}
