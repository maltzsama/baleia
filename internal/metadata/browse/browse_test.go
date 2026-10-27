package browse

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// fakeStore grants one catalog with one table; binding is its default
// binding, if any.
type fakeStore struct {
	catalog db.GetGrantedMetadataCatalogRow
	table   db.GetGrantedMetadataTableRow
	binding *db.GetCatalogBindingForDisplayRow
	tables  []db.ListMetadataTablesPageRow
	// lastPage and lastSearch record the arguments of the last call.
	lastPage   db.ListMetadataTablesPageParams
	lastSearch db.SearchMetadataParams
}

func (f *fakeStore) ListMetadataCatalogs(context.Context, pgtype.UUID) ([]db.ListMetadataCatalogsRow, error) {
	return nil, nil
}

func (f *fakeStore) GetGrantedMetadataCatalog(_ context.Context, arg db.GetGrantedMetadataCatalogParams) (db.GetGrantedMetadataCatalogRow, error) {
	if arg.CatalogID != f.catalog.ID {
		return db.GetGrantedMetadataCatalogRow{}, pgx.ErrNoRows
	}
	return f.catalog, nil
}

func (f *fakeStore) ListChildNamespaces(context.Context, db.ListChildNamespacesParams) ([][]string, error) {
	return [][]string{{"sales"}}, nil
}

func (f *fakeStore) ListMetadataSyncRuns(context.Context, db.ListMetadataSyncRunsParams) ([]db.ListMetadataSyncRunsRow, error) {
	return nil, nil
}

func (f *fakeStore) ListMetadataTablesPage(_ context.Context, arg db.ListMetadataTablesPageParams) ([]db.ListMetadataTablesPageRow, error) {
	f.lastPage = arg
	var out []db.ListMetadataTablesPageRow
	for _, t := range f.tables {
		if t.Name > arg.After && len(out) < int(arg.Lim) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeStore) GetGrantedMetadataTable(_ context.Context, arg db.GetGrantedMetadataTableParams) (db.GetGrantedMetadataTableRow, error) {
	if arg.TableID != f.table.ID {
		return db.GetGrantedMetadataTableRow{}, pgx.ErrNoRows
	}
	return f.table, nil
}

func (f *fakeStore) GetCatalogBindingForDisplay(_ context.Context, arg db.GetCatalogBindingForDisplayParams) (db.GetCatalogBindingForDisplayRow, error) {
	if f.binding == nil || (arg.BindingID.Valid && arg.BindingID != f.binding.ID) {
		return db.GetCatalogBindingForDisplayRow{}, pgx.ErrNoRows
	}
	return *f.binding, nil
}

func (f *fakeStore) ListMetadataColumns(context.Context, pgtype.UUID) ([]db.ListMetadataColumnsRow, error) {
	return []db.ListMetadataColumnsRow{{Name: "id"}}, nil
}

func (f *fakeStore) ListSemanticDocsForTable(context.Context, db.ListSemanticDocsForTableParams) ([]db.SemanticDoc, error) {
	return nil, nil
}

func (f *fakeStore) SearchMetadata(_ context.Context, arg db.SearchMetadataParams) ([]db.SearchMetadataRow, error) {
	f.lastSearch = arg
	return nil, nil
}

func newStore() *fakeStore {
	cat := newID()
	return &fakeStore{
		catalog: db.GetGrantedMetadataCatalogRow{ID: cat},
		table:   db.GetGrantedMetadataTableRow{ID: newID(), CatalogID: cat, Name: "orders"},
		tables:  []db.ListMetadataTablesPageRow{{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}
}

func TestNamespaces_CatalogMustBeGranted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st)
	if _, ns, err := s.Namespaces(ctx, newID(), st.catalog.ID, nil); err != nil || len(ns) != 1 {
		t.Fatalf("Namespaces = %v, %v", ns, err)
	}
	if _, _, err := s.Namespaces(ctx, newID(), newID(), nil); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("ungranted catalog = %v, want ErrCatalogNotFound", err)
	}
}

func TestTables_Pages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st)
	two := 2
	page, err := s.Tables(ctx, newID(), st.catalog.ID, TablesQuery{Limit: &two})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tables) != 2 || page.NextCursor == nil {
		t.Fatalf("first page = %d tables, next %v; want 2 and a cursor", len(page.Tables), page.NextCursor)
	}
	page, err = s.Tables(ctx, newID(), st.catalog.ID, TablesQuery{Limit: &two, Cursor: *page.NextCursor})
	if err != nil || len(page.Tables) != 1 || page.Tables[0].Name != "c" || page.NextCursor != nil {
		t.Fatalf("last page = %+v, %v; want only c and no cursor", page, err)
	}
	if st.lastPage.Namespace == nil {
		t.Error("a nil namespace is sent as the empty path")
	}

	var inv *InvalidError
	zero, big := 0, MaxTablesPage+1
	for name, q := range map[string]TablesQuery{
		"zero limit":   {Limit: &zero},
		"large limit":  {Limit: &big},
		"bad base64":   {Cursor: "%%%"},
		"invalid utf8": {Cursor: base64.RawURLEncoding.EncodeToString([]byte{0xff})},
	} {
		if _, err := s.Tables(ctx, newID(), st.catalog.ID, q); !errors.As(err, &inv) {
			t.Errorf("%s = %v, want an InvalidError", name, err)
		}
	}
	// The catalog is checked before the parameters.
	if _, err := s.Tables(ctx, newID(), newID(), TablesQuery{Limit: &zero}); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("ungranted catalog with a bad limit = %v, want ErrCatalogNotFound", err)
	}
}

func TestTable_Binding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st)

	got, err := s.Table(ctx, newID(), st.table.ID, pgtype.UUID{})
	if err != nil || got.Binding != nil || len(got.Columns) != 1 {
		t.Fatalf("no binding = %+v, %v; want the table without a binding", got, err)
	}
	if _, err := s.Table(ctx, newID(), st.table.ID, newID()); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("unknown binding = %v, want ErrBindingNotFound", err)
	}
	st.binding = &db.GetCatalogBindingForDisplayRow{ID: newID()}
	if got, err := s.Table(ctx, newID(), st.table.ID, pgtype.UUID{}); err != nil || got.Binding == nil {
		t.Errorf("default binding = %+v, %v", got.Binding, err)
	}
	if _, err := s.Table(ctx, newID(), newID(), pgtype.UUID{}); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("unknown table = %v, want ErrTableNotFound", err)
	}
}

func TestSearch_Validates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st)
	if _, err := s.Search(ctx, newID(), SearchQuery{Text: "ord"}); err != nil {
		t.Fatal(err)
	}
	if st.lastSearch.Kind != KindAll || st.lastSearch.Lim != DefaultSearch {
		t.Errorf("defaults = kind %q, limit %d", st.lastSearch.Kind, st.lastSearch.Lim)
	}
	var inv *InvalidError
	zero := 0
	for name, q := range map[string]SearchQuery{
		"short":     {Text: "o"},
		"bad kind":  {Text: "ord", Kind: "view"},
		"bad limit": {Text: "ord", Limit: &zero},
	} {
		if _, err := s.Search(ctx, newID(), q); !errors.As(err, &inv) {
			t.Errorf("%s = %v, want an InvalidError", name, err)
		}
	}
}
