package search

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

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
	// Ordered by title, case-insensitively.
	if hits[0].Title != "Revenue" || hits[1].Title != "revenue by month" || hits[2].Title != "Revenue reports" {
		t.Errorf("order = %q, %q, %q", hits[0].Title, hits[1].Title, hits[2].Title)
	}
	q := hits[1]
	if q.Kind != KindSavedQuery || len(q.FolderPath) != 2 || q.FolderPath[0].Name != "Reports" || q.FolderPath[1].Name != "Revenue reports" || q.SpaceName != "Finance" {
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
