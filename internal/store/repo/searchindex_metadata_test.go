package repo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

func (f *workbenchFixture) searchTable(t *testing.T, name, comment string, columns ...string) db.UpsertMetadataTableRow {
	t.Helper()
	ctx := context.Background()
	row, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: name, Kind: "table",
		Fingerprint: "fp-" + name + comment, Properties: []byte(`{}`), Comment: &comment,
	})
	if err != nil {
		t.Fatalf("UpsertMetadataTable(%s): %v", name, err)
	}
	if len(columns) == 0 {
		return row
	}
	ordinals, types := make([]int32, len(columns)), make([]string, len(columns))
	for i := range columns {
		ordinals[i], types[i] = int32(i+1), "string"
	}
	if err := f.r.InsertMetadataColumns(ctx, db.InsertMetadataColumnsParams{
		TableID: row.ID, Names: columns, Ordinals: ordinals, NativeTypes: types, TypeSystem: "iceberg",
	}); err != nil {
		t.Fatalf("InsertMetadataColumns(%s): %v", name, err)
	}
	return row
}

func (f *workbenchFixture) searchMetadata(t *testing.T, q string) []db.SearchMetadataRow {
	t.Helper()
	rows, err := f.r.SearchMetadata(context.Background(), db.SearchMetadataParams{WorkspaceID: f.wsPG(), Q: q, Kind: "all", Lim: 20})
	if err != nil {
		t.Fatalf("SearchMetadata(%q): %v", q, err)
	}
	return rows
}

func names(rows []db.SearchMetadataRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.TableName
		if r.ColumnName != "" {
			out[i] += "." + r.ColumnName
		}
	}
	return out
}

func TestSearchIndex_RebuildsWhenTheSourceChanges(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	f.searchTable(t, "orders", "Pedidos de venda", "order_note")
	if got := f.searchMetadata(t, "pedidos"); len(got) != 1 || got[0].TableName != "orders" {
		t.Fatalf("a comment word finds its table: %v", names(got))
	}
	if got := f.searchMetadata(t, "invoices"); len(got) != 0 {
		t.Fatalf("nothing matches yet: %v", names(got))
	}

	f.searchTable(t, "invoices", "", "total")
	if got := f.searchMetadata(t, "invoices"); len(got) != 1 {
		t.Fatalf("a table added after the index was built is found: %v", names(got))
	}

	if err := f.gdb.Model(&model.MetadataTable{}).Where("catalog_id = ? AND name = ?", string(f.catalog), "orders").
		Update("removed_at", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if got := f.searchMetadata(t, "orders"); len(got) != 0 {
		t.Fatalf("a removed table is not found: %v", names(got))
	}
}

func TestSearchIndex_StaleTermsDoNotLeakAcrossGrants(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	f.searchTable(t, "orders", "", "id")
	if got := f.searchMetadata(t, "orders"); len(got) != 1 {
		t.Fatalf("granted catalog: %v", names(got))
	}
	if err := f.gdb.Where("workspace_id = ?", string(f.ws)).Delete(&model.WorkspaceCatalogGrant{}).Error; err != nil {
		t.Fatal(err)
	}
	var terms int64
	f.gdb.Model(&model.SearchTerm{}).Where("scope_key = ?", metadataScopeKey(string(f.catalog))).Count(&terms)
	if terms == 0 {
		t.Fatal("the terms outlive the grant: that is the point of this test")
	}
	if got := f.searchMetadata(t, "orders"); len(got) != 0 {
		t.Fatalf("a catalog no longer granted must not be searched: %v", names(got))
	}
}

func TestSearchIndex_FindsMisspellingsAndPrefixes(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	f.searchTable(t, "customers", "Cadastro de clientes", "customerLifetimeValue")
	for _, q := range []string{"custmer", "CUSTO", "lifetime value", "clientes", "clientes cadastro"} {
		if got := f.searchMetadata(t, q); len(got) == 0 {
			t.Errorf("Search(%q) found nothing", q)
		}
	}
	if got := f.searchMetadata(t, "zzzzzz"); len(got) != 0 {
		t.Errorf("an unrelated query finds %v", names(got))
	}
}

func TestSearchIndex_ConcurrentRefresh(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	f.searchTable(t, "orders", "Pedidos", "id", "order_note")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.r.refreshMetadataScope(ctx, string(f.catalog))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("refreshMetadataScope: %v", err)
		}
	}
	var got int64
	f.gdb.Model(&model.SearchTerm{}).Where("scope_key = ?", metadataScopeKey(string(f.catalog))).Count(&got)
	want := int64(len(search.Terms("orders", "Pedidos")) + len(search.Terms("id")) + len(search.Terms("order_note")))
	if got != want {
		t.Fatalf("%d terms, want %d", got, want)
	}
}

func TestSearchIndex_SyncRefreshesTheScope(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()
	run, err := f.r.StartMetadataSyncRun(ctx, db.StartMetadataSyncRunParams{CatalogID: f.catalogPG(), SourcePath: "provider"})
	if err != nil {
		t.Fatal(err)
	}
	f.searchTable(t, "orders", "", "id")
	if err := f.r.FinishMetadataSyncRun(ctx, db.FinishMetadataSyncRunParams{ID: run.ID, Status: "finished", TablesSeen: i32(1)}); err != nil {
		t.Fatal(err)
	}
	var scope model.SearchScope
	if err := f.gdb.Where("scope_key = ?", metadataScopeKey(string(f.catalog))).First(&scope).Error; err != nil || scope.IndexedAt == nil {
		t.Fatalf("scope after the sync: %+v, %v", scope, err)
	}
	var terms int64
	f.gdb.Model(&model.SearchTerm{}).Where("scope_key = ? AND term = ?", scope.ScopeKey, "orders").Count(&terms)
	if terms != 1 {
		t.Fatalf("the table's term is indexed: %d", terms)
	}
}

// A scope larger than one page of tables is built without holding a cursor
// while its terms are written: the build once failed with "bad connection".
func TestSearchIndex_BuildsScopesLargerThanAPage(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	now := time.Now()
	tables := make([]model.MetadataTable, indexPageSize+1)
	var columns []model.MetadataColumn
	for i := range tables {
		id := model.UUID(NewUUID())
		tables[i] = model.MetadataTable{
			ID: id, CatalogID: f.catalog, Namespace: model.StringArray{"big"}, Name: model.Text(fmt.Sprintf("bigtable%05d", i)),
			Kind: "table", Fingerprint: "f", LastSeenAt: now, LastChangedAt: now,
		}
		columns = append(columns, model.MetadataColumn{TableID: id, Ordinal: 1, Name: "amount", NativeType: "long", TypeSystem: "iceberg"})
	}
	if err := f.gdb.CreateInBatches(&tables, 1000).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.CreateInBatches(&columns, 1000).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.r.refreshMetadataScope(context.Background(), string(f.catalog)); err != nil {
		t.Fatalf("refreshMetadataScope: %v", err)
	}
	var found int64
	err := f.gdb.Model(&model.SearchTerm{}).
		Where("scope_key = ? AND kind = ? AND term = ?", metadataScopeKey(string(f.catalog)), kindTable, fmt.Sprintf("bigtable%05d", indexPageSize)).
		Count(&found).Error
	if err != nil || found != 1 {
		t.Fatalf("the last table of the last page is indexed: %d, %v", found, err)
	}
	var withColumns int64
	f.gdb.Model(&model.SearchTerm{}).Where("scope_key = ? AND kind = ? AND term = ?", metadataScopeKey(string(f.catalog)), kindColumn, "amount").Count(&withColumns)
	if withColumns != int64(len(tables)) {
		t.Fatalf("%d columns indexed, want %d", withColumns, len(tables))
	}
}

// The fuzzy fallback reads a scope's vocabulary from the database once, not on
// every search, and reads it again only when the scope was rebuilt.
func TestSearchIndex_FuzzyReadsTheVocabularyOnce(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	var reads atomic.Int64
	err := f.gdb.Callback().Query().After("gorm:query").Register("count_vocabulary_reads", func(tx *gorm.DB) {
		// The quote of an identifier is the backend's: PostgreSQL's or MySQL's.
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, `DISTINCT "term"`) || strings.Contains(sql, "DISTINCT `term`") {
			reads.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	f.searchTable(t, "customers", "", "id")

	for range 3 {
		if got := f.searchMetadata(t, "custmer"); len(got) == 0 {
			t.Fatal("a misspelling finds the table")
		}
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("the vocabulary was read %d times by 3 searches, want 1", n)
	}

	// A sync wrote a new table: the scope is rebuilt, its version changes and
	// the next fuzzy search reads the vocabulary again, and finds the new term.
	f.searchTable(t, "invoices", "", "total")
	if got := f.searchMetadata(t, "invoises"); len(got) == 0 || got[0].TableName != "invoices" {
		t.Fatalf("a misspelling of a table added after the vocabulary was cached: %v", names(got))
	}
	if n := reads.Load(); n != 2 {
		t.Fatalf("the vocabulary was read %d times after a rebuild, want 2", n)
	}

	// A search that forgives typos reads the terms of the prefix it needs once
	// per version of the scope: "cus" starts with "cu", not read since the
	// rebuild; asking again reads nothing.
	f.searchMetadata(t, "cus")
	if n := reads.Load(); n != 3 {
		t.Fatalf("the prefix cu at the new version: %d reads, want 3", n)
	}
	f.searchMetadata(t, "cus")
	if n := reads.Load(); n != 3 {
		t.Fatalf("the prefix was read again: %d reads, want 3", n)
	}
}
