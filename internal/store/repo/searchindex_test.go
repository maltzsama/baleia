package repo

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// The content index proposes a saved query by the words of its name, slug and
// SQL, and follows a rename.
func TestSearchIndex_ContentCandidates(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()
	query := model.SavedQuery{
		ID: model.UUID(NewUUID()), WorkspaceID: f.ws, SpaceID: f.space, CreatedBy: f.owner,
		Name: "Monthly report", Slug: "monthly_report", SQL: "SELECT * FROM sales.refunds",
	}
	if err := f.gdb.Create(&query).Error; err != nil {
		t.Fatal(err)
	}
	asked := func(q string) []db.UUID {
		t.Helper()
		got, err := f.r.ContentCandidates(ctx, f.wsPG(), search.Compile(q))
		if err != nil {
			t.Fatalf("ContentCandidates(%q): %v", q, err)
		}
		return got.SavedQueries
	}
	if got := asked("refunds"); len(got) != 1 || UUIDString(got[0]) != string(query.ID) {
		t.Fatalf("a word of the SQL proposes the query: %v", got)
	}
	if got := asked("zzzzzz"); len(got) != 0 {
		t.Fatalf("an unrelated word proposes %v", got)
	}

	if err := f.gdb.Model(&model.SavedQuery{}).Where("id = ?", string(query.ID)).
		Updates(map[string]any{"name": "Quarterly numbers", "slug": "quarterly_numbers", "updated_at": time.Now().UTC().Add(time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	if got := asked("quarterly"); len(got) != 1 {
		t.Fatalf("a renamed query is found by its new name: %v", got)
	}
	if got := asked("monthly"); len(got) != 0 {
		t.Fatalf("and no longer by the old one: %v", got)
	}
}

// The index SQL (the scope lock, batched inserts, exact, prefix and fuzzy
// lookups) runs on every backend. It uses a scope with no owner, so it needs
// none of the fixtures the other tests build.
func TestSearchIndex_PortableLookups(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	r := New(gdb)
	ctx := context.Background()
	const key = "metadata:portable"
	rows := []indexRow{
		{Kind: kindTable, Ref: "t1", Texts: []string{"customers", "Cadastro de clientes"}},
		{Kind: kindTable, Ref: "t2", Texts: []string{"orders_archive"}},
		{Kind: kindColumn, Ref: "t1:1", Texts: []string{"customerLifetimeValue"}},
	}
	// Enough rows to flush the insert batch more than once.
	for i := range 700 {
		rows = append(rows, indexRow{Kind: kindColumn, Ref: columnRef("t3", int32(i)), Texts: []string{"filler_column", "col" + string(rune('a'+i%26))}})
	}
	builder := scopeBuilder{
		fingerprint: func(*gorm.DB) (string, error) { return "v1", nil },
		rows: func(_ *gorm.DB, emit func(indexRow) error) error {
			for _, row := range rows {
				if err := emit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}
	var indexedAt []*time.Time
	for range 2 { // the second run finds the scope fresh and does nothing
		if err := r.refreshScope(ctx, key, scopeOwner{}, builder); err != nil {
			t.Fatalf("refreshScope: %v", err)
		}
		var scope model.SearchScope
		if err := gdb.Where("scope_key = ?", key).Take(&scope).Error; err != nil || scope.IndexedAt == nil {
			t.Fatalf("scope after refresh: %+v, %v", scope, err)
		}
		indexedAt = append(indexedAt, scope.IndexedAt)
		time.Sleep(20 * time.Millisecond)
	}
	if !indexedAt[0].Equal(*indexedAt[1]) {
		t.Fatalf("a fresh scope was rebuilt: indexed at %v, then %v", indexedAt[0], indexedAt[1])
	}
	var n int64
	gdb.Model(&model.SearchTerm{}).Where("scope_key = ?", key).Count(&n)
	if n < 700 {
		t.Fatalf("%d terms stored", n)
	}

	find := func(q string, kinds ...string) map[string][]string {
		t.Helper()
		var found map[string][]string
		err := gdb.Transaction(func(tx *gorm.DB) error {
			var err error
			found, err = r.candidates(tx, []string{key}, kinds, search.Compile(q), 20, true)
			return err
		})
		if err != nil {
			t.Fatalf("candidates(%q): %v", q, err)
		}
		return found
	}
	tests := []struct {
		name  string
		query string
		kind  string
		want  string
	}{
		{name: "exact word", query: "CLIENTES", kind: kindTable, want: "t1"},
		{name: "singular of a plural", query: "customer", kind: kindTable, want: "t1"},
		{name: "prefix", query: "arch", kind: kindTable, want: "t2"},
		{name: "camel case word", query: "lifetime", kind: kindColumn, want: "t1:1"},
		{name: "misspelling", query: "custmer", kind: kindTable, want: "t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := find(tt.query, tt.kind)[tt.kind]
			if !slices.Contains(got, tt.want) {
				t.Errorf("candidates(%q) = %v, want it to hold %q", tt.query, got, tt.want)
			}
		})
	}
	if got := find("zzzzzz", kindTable, kindColumn); len(got) != 0 {
		t.Errorf("an unrelated query proposes %v", got)
	}
}

// On PostgreSQL the terms compare by bytes, which is what lets a btree index
// serve LIKE 'prefix%' on a database collated en_US: "Z" sorts before "a".
func TestSearchIndex_TermsCompareByBytesOnPostgres(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	if gdb.Name() != "postgres" {
		t.Skip("only PostgreSQL needs the collation")
	}
	if err := gdb.Create(&model.SearchScope{ScopeKey: "metadata:bytes"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"a", "Z", "b"} {
		if err := gdb.Create(&model.SearchTerm{ScopeKey: "metadata:bytes", Term: model.Term(term), Kind: kindTable, RefID: "t"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	if err := gdb.Model(&model.SearchTerm{}).Where("scope_key = ?", "metadata:bytes").Order("term").Pluck("term", &got).Error; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"Z", "a", "b"}) {
		t.Fatalf("terms in order = %v, want byte order [Z a b]", got)
	}
}
