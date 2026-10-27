package api

import "testing"

// A limit caps the filtered list and an out-of-range value is rejected (the
// workbench palette relies on both: #332, #333).
func TestListLimits(t *testing.T) {
	t.Parallel()
	t.Run("semantic metrics", func(t *testing.T) {
		t.Parallel()
		f, _ := metricFixture(t)
		ctx := f.as(f.owner)

		createMetric(t, f, ctx, SemanticMetricCreate{
			Slug: "net_revenue", Label: "Net revenue", CatalogId: f.lake,
			Namespace: []string{"sales"}, Table: "orders",
			Definition: simpleDefinition("sum", "net_revenue"),
		})
		createMetric(t, f, ctx, SemanticMetricCreate{
			Slug: "gross_revenue", Label: "Gross revenue", CatalogId: f.lake,
			Namespace: []string{"sales"}, Table: "orders",
			Definition: simpleDefinition("sum", "gross_revenue"),
		})

		lim := 1
		list, err := f.h.ListSemanticMetrics(ctx, ListSemanticMetricsRequestObject{
			Id: f.ws, Params: ListSemanticMetricsParams{Limit: &lim},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := list.(ListSemanticMetrics200JSONResponse); len(got) != 1 {
			t.Fatalf("limit=1 returned %d metrics", len(got))
		}

		bad := 0
		if r, err := f.h.ListSemanticMetrics(ctx, ListSemanticMetricsRequestObject{
			Id: f.ws, Params: ListSemanticMetricsParams{Limit: &bad},
		}); err != nil {
			t.Fatal(err)
		} else if _, ok := r.(ListSemanticMetrics422JSONResponse); !ok {
			t.Fatalf("limit=0: %T", r)
		}
	})

	t.Run("saved queries", func(t *testing.T) {
		t.Parallel()
		f := newMetaFixture(t)
		ctx := f.as(f.owner)

		for _, name := range []string{"Revenue", "Costs"} {
			if _, err := f.h.CreateSavedQuery(ctx, CreateSavedQueryRequestObject{
				Id: f.ws, Body: &CreateSavedQueryRequest{Name: name, Sql: "SELECT 1"},
			}); err != nil {
				t.Fatal(err)
			}
		}

		lim := 1
		list, err := f.h.ListSavedQueries(ctx, ListSavedQueriesRequestObject{
			Id: f.ws, Params: ListSavedQueriesParams{Limit: &lim},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := list.(ListSavedQueries200JSONResponse); len(got) != 1 {
			t.Fatalf("limit=1 returned %d queries", len(got))
		}

		bad := 0
		if r, err := f.h.ListSavedQueries(ctx, ListSavedQueriesRequestObject{
			Id: f.ws, Params: ListSavedQueriesParams{Limit: &bad},
		}); err != nil {
			t.Fatal(err)
		} else if _, ok := r.(ListSavedQueries422JSONResponse); !ok {
			t.Fatalf("limit=0: %T", r)
		}
	})
}
