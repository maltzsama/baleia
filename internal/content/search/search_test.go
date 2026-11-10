package search

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

func newID() db.UUID { return db.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	space   db.ListSpacesForPrincipalRow
	folders []db.QueryFolder
	queries []db.SavedQuery
	metrics []db.SemanticMetric
	models  []db.SemanticModel
}

func (f *fakeStore) ListSpacesForPrincipal(context.Context, db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error) {
	return []db.ListSpacesForPrincipalRow{f.space}, nil
}

func (f *fakeStore) ListAllVisibleQueryFolders(context.Context, db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error) {
	return f.folders, nil
}

func (f *fakeStore) SearchVisibleSavedQueries(context.Context, db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error) {
	return f.queries, nil
}

func (f *fakeStore) ListVisibleSemanticMetrics(context.Context, db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error) {
	return f.metrics, nil
}

func (f *fakeStore) ListVisibleSemanticModels(context.Context, db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error) {
	return f.models, nil
}

func TestSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	space := db.ListSpacesForPrincipalRow{ID: newID(), Name: "Finance"}
	root := db.QueryFolder{ID: newID(), SpaceID: space.ID, Name: "Reports", Slug: "reports"}
	sub := db.QueryFolder{ID: newID(), SpaceID: space.ID, ParentID: root.ID, Name: "Revenue reports", Slug: "revenue_reports"}
	st := &fakeStore{
		space:   space,
		folders: []db.QueryFolder{root, sub},
		queries: []db.SavedQuery{{ID: newID(), SpaceID: space.ID, FolderID: sub.ID, Name: "revenue by month", Slug: "revenue_by_month"}},
		metrics: []db.SemanticMetric{{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue", Certified: true}, {Label: "Orders", Slug: "orders"}},
		models:  []db.SemanticModel{{ID: newID(), Label: "Customers", Slug: "customers"}},
	}
	s := New(st)

	hits, err := s.Search(ctx, newID(), newID(), Query{Text: " reven "})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %+v; want the folder, the query and the metric", hits)
	}
	// Best first: the certified metric, whose whole name starts with the query.
	if hits[0].Title != "Revenue" {
		t.Errorf("order = %q, %q, %q", hits[0].Title, hits[1].Title, hits[2].Title)
	}
	var q Hit
	for _, h := range hits {
		if h.Kind == KindSavedQuery {
			q = h
		}
	}
	if len(q.FolderPath) != 2 || q.FolderPath[0].Name != "Reports" || q.FolderPath[1].Name != "Revenue reports" || q.SpaceName != "Finance" {
		t.Errorf("query hit = %+v; want its folder path from the root and its space name", q)
	}
	if hits[0].Certified == nil || !*hits[0].Certified {
		t.Error("a semantic hit carries its certification")
	}

	if hits, _ := s.Search(ctx, newID(), newID(), Query{Text: "reven", Kind: KindSemanticMetric}); len(hits) != 1 {
		t.Errorf("kind filter = %d hits, want 1", len(hits))
	}
	if hits, _ := s.Search(ctx, newID(), newID(), Query{Text: "r"}); len(hits) != 0 {
		t.Error("a one-character query has no hits")
	}
	if hits, _ := s.Search(ctx, newID(), newID(), Query{Text: "re", Limit: 1}); len(hits) != 1 {
		t.Errorf("limit = %d hits, want 1", len(hits))
	}
}

// The best hits survive the limit: hits are ranked before the list is cut,
// not sorted by title and then cut.
func TestSearch_RanksBeforeLimit(t *testing.T) {
	t.Parallel()
	space := db.ListSpacesForPrincipalRow{ID: newID(), Name: "Finance"}
	st := &fakeStore{space: space}
	for i := range 25 {
		st.queries = append(st.queries, db.SavedQuery{
			ID: newID(), SpaceID: space.ID, Name: fmt.Sprintf("A revenue backup %02d", i), Slug: fmt.Sprintf("a_revenue_backup_%02d", i),
		})
	}
	st.metrics = []db.SemanticMetric{{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue"}}

	hits, err := New(st).Search(context.Background(), newID(), newID(), Query{Text: "revenue", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 20 || hits[0].Kind != KindSemanticMetric || hits[0].Title != "Revenue" {
		t.Fatalf("first of %d hits = %+v; want the exact metric", len(hits), hits[0])
	}
}

func TestSearch_FoldsCaseAndAccents(t *testing.T) {
	t.Parallel()
	space := db.ListSpacesForPrincipalRow{ID: newID(), Name: "Finance"}
	st := &fakeStore{
		space:   space,
		metrics: []db.SemanticMetric{{ID: newID(), SpaceID: space.ID, Label: "Receita líquida", Slug: "receita_liquida"}},
		models:  []db.SemanticModel{{ID: newID(), SpaceID: space.ID, Label: "Sessões web", Slug: "web"}},
	}
	s := New(st)
	for _, text := range []string{"RECEITA LIQUIDA", "liquida receita", "sessao"} {
		hits, err := s.Search(context.Background(), newID(), newID(), Query{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("Search(%q) = %+v; want one hit", text, hits)
		}
	}
}

// Certified, own and recent content ranks first among equal matches.
func TestSearch_Boosts(t *testing.T) {
	t.Parallel()
	space := db.ListSpacesForPrincipalRow{ID: newID(), Name: "Finance"}
	user := newID()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	old := db.Timestamptz{Time: now.AddDate(-1, 0, 0), Valid: true}
	fresh := db.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	tests := []struct {
		name    string
		store   *fakeStore
		kind    string
		boosted string
	}{
		{
			name: "certified",
			kind: KindSemanticMetric,
			store: &fakeStore{space: space, metrics: []db.SemanticMetric{
				{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue_a", UpdatedAt: old},
				{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue_b", UpdatedAt: old, Certified: true},
			}},
			boosted: "revenue_b",
		},
		{
			name: "own",
			kind: KindSavedQuery,
			store: &fakeStore{space: space, queries: []db.SavedQuery{
				{ID: newID(), SpaceID: space.ID, Name: "Revenue", Slug: "revenue_a", UpdatedAt: old, CreatedBy: newID()},
				{ID: newID(), SpaceID: space.ID, Name: "Revenue", Slug: "revenue_b", UpdatedAt: old, CreatedBy: user},
			}},
			boosted: "revenue_b",
		},
		{
			name: "recent",
			kind: KindSemanticModel,
			store: &fakeStore{space: space, models: []db.SemanticModel{
				{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue_a", UpdatedAt: old},
				{ID: newID(), SpaceID: space.ID, Label: "Revenue", Slug: "revenue_b", UpdatedAt: fresh},
			}},
			boosted: "revenue_b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := New(tt.store)
			s.now = func() time.Time { return now }
			hits, err := s.Search(context.Background(), newID(), user, Query{Text: "revenue", Kind: tt.kind})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 2 || hits[0].Slug != tt.boosted || hits[0].Score <= hits[1].Score {
				t.Fatalf("hits = %+v; want %s first", hits, tt.boosted)
			}
		})
	}
}
