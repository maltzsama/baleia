package api

import (
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/metrics"
)

func federatedFixture(t *testing.T) *metaFixture {
	t.Helper()
	f := newMetaFixture(t)
	seedGoldenCorpus(t, f, readGoldenCorpus(t))
	return f
}

func searchAll(t *testing.T, f *metaFixture, user string, params SearchWorkspaceParams) SearchWorkspace200JSONResponse {
	t.Helper()
	r, err := f.h.SearchWorkspace(f.as(user), SearchWorkspaceRequestObject{Id: f.ws, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := r.(SearchWorkspace200JSONResponse)
	if !ok {
		t.Fatalf("SearchWorkspace(%+v) = %T", params, r)
	}
	return res
}

func kindsOf(res SearchWorkspace200JSONResponse) []WorkspaceSearchKind {
	out := make([]WorkspaceSearchKind, len(res.Items))
	for i, item := range res.Items {
		out[i] = item.Kind
	}
	return out
}

// One query finds every kind, best first, with a facet per kind that matched.
func TestSearchWorkspace_MergesEveryKind(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "clientes"})
	got := map[WorkspaceSearchKind]bool{}
	for _, item := range res.Items {
		got[item.Kind] = true
	}
	for _, kind := range []WorkspaceSearchKind{WorkspaceSearchKindTable, WorkspaceSearchKindDoc, WorkspaceSearchKindSemanticModel} {
		if !got[kind] {
			t.Errorf("no %s among %v", kind, kindsOf(res))
		}
	}
	for i := 1; i < len(res.Items); i++ {
		if res.Items[i].Score > res.Items[i-1].Score {
			t.Fatalf("not best first at %d: %v then %v", i, res.Items[i-1].Score, res.Items[i].Score)
		}
	}
	facets := map[WorkspaceSearchKind]int{}
	for _, f := range res.Facets {
		facets[f.Kind] = f.Count
	}
	if facets[WorkspaceSearchKindTable] == 0 || facets[WorkspaceSearchKindSemanticModel] != 1 {
		t.Errorf("facets = %v", res.Facets)
	}
	for _, item := range res.Items {
		switch item.Kind {
		case WorkspaceSearchKindTable, WorkspaceSearchKindColumn:
			if item.Metadata == nil || item.Doc != nil || item.Content != nil {
				t.Errorf("%s carries %+v", item.Kind, item)
			}
		case WorkspaceSearchKindDoc:
			if item.Doc == nil || item.Metadata != nil {
				t.Errorf("doc carries %+v", item)
			}
		default:
			if item.Content == nil || item.Metadata != nil {
				t.Errorf("%s carries %+v", item.Kind, item)
			}
		}
		if item.Id == "" || item.Title == "" {
			t.Errorf("incomplete item %+v", item)
		}
	}
	if len(res.ExpandedWith) != 0 {
		t.Errorf("expanded_with = %v without a glossary", res.ExpandedWith)
	}
}

func TestSearchWorkspace_KindFilterAndLimit(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)

	tables := []WorkspaceSearchKind{WorkspaceSearchKindTable}
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "customers", Kind: &tables})
	if len(res.Items) == 0 || slices.ContainsFunc(res.Items, func(i WorkspaceSearchItem) bool { return i.Kind != WorkspaceSearchKindTable }) {
		t.Fatalf("kind=table returned %v", kindsOf(res))
	}
	if len(res.Facets) != 1 || res.Facets[0].Kind != WorkspaceSearchKindTable {
		t.Errorf("facets of a filtered search = %v", res.Facets)
	}

	limit := 2
	res = searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "revenue", Limit: &limit})
	if len(res.Items) != 2 {
		t.Fatalf("limit 2 returned %d", len(res.Items))
	}
	total := 0
	for _, facet := range res.Facets {
		total += facet.Count
	}
	if total <= 2 {
		t.Errorf("facets count the candidates before the limit, got %d in total: %v", total, res.Facets)
	}
	if res.Items[0].Kind != WorkspaceSearchKindSemanticMetric {
		t.Errorf("the exact metric ranks first: %v", kindsOf(res))
	}
}

func TestSearchWorkspace_Validation(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	zero, tooMany := 0, 51
	badKind := []WorkspaceSearchKind{"nope"}
	for name, params := range map[string]SearchWorkspaceParams{
		"short query":  {Q: "a"},
		"limit zero":   {Q: "orders", Limit: &zero},
		"limit high":   {Q: "orders", Limit: &tooMany},
		"unknown kind": {Q: "orders", Kind: &badKind},
	} {
		r, err := f.h.SearchWorkspace(f.as(f.owner), SearchWorkspaceRequestObject{Id: f.ws, Params: params})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := r.(SearchWorkspace422JSONResponse); !ok {
			t.Errorf("%s = %T, want 422", name, r)
		}
	}
}

// What a caller may not see is not found, on every surface at once.
func TestSearchWorkspace_Visibility(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	r, err := f.h.SearchWorkspace(f.as(f.stranger), SearchWorkspaceRequestObject{Id: f.ws, Params: SearchWorkspaceParams{Q: "orders"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(SearchWorkspace404JSONResponse); !ok {
		t.Fatalf("a stranger gets %T, want 404", r)
	}
	// The ungranted catalog's table never shows up, whatever the query.
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "orders_secret"})
	for _, item := range res.Items {
		if item.Metadata != nil && item.Metadata.Table == "orders_secret" {
			t.Fatalf("a table of an ungranted catalog was returned: %+v", item)
		}
	}
}

func TestSearchWorkspace_CountsEmptyResults(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	reg := metrics.New("api")
	f.h.metrics.search = reg.Search()
	searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "orders"})
	searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "zzzzzz"})
	families, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "baleia_search_requests_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			got[labels["kind"]+"/"+labels["outcome"]] = m.GetCounter().GetValue()
		}
	}
	if got["federated/hit"] != 1 || got["federated/empty"] != 1 {
		t.Errorf("baleia_search_requests_total = %v", got)
	}
}

// A kind filter applies before a surface cuts its results to the limit: the
// metric that ranks first for "revenue" does not take a place from the saved
// queries that were asked for.
func TestSearchWorkspace_KindFilterKeepsTheLimitFull(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	kinds := []WorkspaceSearchKind{WorkspaceSearchKindSavedQuery}
	limit := 5
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "revenue", Kind: &kinds, Limit: &limit})
	if len(res.Items) != limit {
		t.Fatalf("%d items for kind=saved_query limit=%d: %v", len(res.Items), limit, kindsOf(res))
	}
	for _, item := range res.Items {
		if item.Kind != WorkspaceSearchKindSavedQuery {
			t.Fatalf("kind=saved_query returned %s", item.Kind)
		}
	}
	// Two kinds are two searches, each with its own limit.
	kinds = []WorkspaceSearchKind{WorkspaceSearchKindSavedQuery, WorkspaceSearchKindSemanticMetric}
	res = searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "revenue", Kind: &kinds, Limit: &limit})
	got := map[WorkspaceSearchKind]int{}
	for _, f := range res.Facets {
		got[f.Kind] = f.Count
	}
	if got[WorkspaceSearchKindSemanticMetric] == 0 || got[WorkspaceSearchKindSavedQuery] < limit {
		t.Errorf("facets = %v", res.Facets)
	}
}

// catalog_id narrows docs before their limit applies.
func TestSearchWorkspace_CatalogFilterNarrowsDocs(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	docs := []WorkspaceSearchKind{WorkspaceSearchKindDoc}
	in := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "faturamento", Kind: &docs, CatalogId: &f.lake})
	if len(in.Items) == 0 {
		t.Fatal("the docs of the catalog are found")
	}
	other := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "faturamento", Kind: &docs, CatalogId: &f.other})
	if len(other.Items) != 0 || len(other.Facets) != 0 {
		t.Errorf("another catalog has no docs: %v %v", kindsOf(other), other.Facets)
	}
}
