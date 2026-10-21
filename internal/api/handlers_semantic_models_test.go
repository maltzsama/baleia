package api

import (
	"strings"
	"testing"
)

// The definition a client sends carries neither the slug nor the binding, and
// a rename leaves the stored slug stale; the explore compiler must still find
// the model by the row's slug (#121).
func TestSemanticModels_CompileResolvesModelByRowSlug(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)

	definition := map[string]interface{}{
		"source":      map[string]interface{}{"kind": "table", "namespace": []string{"sales"}, "table": "orders"},
		"primary_key": []string{"id"},
		"dimensions": []map[string]interface{}{
			{"name": "id", "type": "int64"},
			{"name": "status", "type": "string"},
		},
		"metrics": []map[string]interface{}{{"name": "order_count", "aggregation": "count"}},
	}
	created, err := f.h.CreateSemanticModel(ctx, CreateSemanticModelRequestObject{
		Id: f.ws,
		Body: &SemanticModelCreate{
			Slug: "orders", Label: "Orders", CatalogId: f.lake, Definition: definition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, ok := created.(CreateSemanticModel201JSONResponse)
	if !ok {
		t.Fatalf("create: %T", created)
	}

	compile := func(slug string) CompileExploreQueryResponseObject {
		t.Helper()
		metrics := []string{"order_count"}
		dimensions := []string{"status"}
		r, err := f.h.CompileExploreQuery(ctx, CompileExploreQueryRequestObject{
			Id: f.ws,
			Body: &ExploreCompileRequest{
				Query: ExploreQuery{Model: slug, Dimensions: &dimensions, Metrics: &metrics},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	out, ok := compile("orders").(CompileExploreQuery200JSONResponse)
	if !ok {
		t.Fatalf("compile by the created slug did not succeed")
	}
	if !strings.Contains(out.Sql, "orders") || !strings.Contains(out.Sql, "COUNT") {
		t.Fatalf("sql = %s", out.Sql)
	}

	// Renaming the model must not strand the explore query on the old slug.
	renamed := "orders_v2"
	if _, err := f.h.UpdateSemanticModel(ctx, UpdateSemanticModelRequestObject{
		Id: f.ws, ModelId: model.Id, Body: &SemanticModelUpdate{Slug: &renamed},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := compile(renamed).(CompileExploreQuery200JSONResponse); !ok {
		t.Fatalf("compile by the renamed slug did not succeed")
	}
	if _, ok := compile("orders").(CompileExploreQuery404JSONResponse); !ok {
		t.Fatalf("the old slug should no longer resolve")
	}
}
