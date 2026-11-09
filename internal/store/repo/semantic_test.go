package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/store/model"
)

// semanticFixture is the workbench fixture with its granted catalog, binding
// and engine enabled, so metadata and semantic reads see it.
func semanticFixture(t *testing.T) *workbenchFixture {
	t.Helper()
	f := newWorkbenchFixture(t)
	if err := f.gdb.Model(&model.Catalog{}).Where("id = ?", string(f.catalog)).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable catalog: %v", err)
	}
	if err := f.gdb.Model(&model.CatalogBinding{}).Where("id = ?", string(f.binding)).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable binding: %v", err)
	}
	if err := f.gdb.Model(&model.Engine{}).Where("id = ?", string(f.engine)).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable engine: %v", err)
	}
	return f
}

func (f *workbenchFixture) ownerPG() db.UUID { return PGUUID(string(f.owner)) }
func (f *workbenchFixture) memberPG() db.UUID {
	return PGUUID(string(f.member))
}
func (f *workbenchFixture) wsPG() db.UUID      { return PGUUID(string(f.ws)) }
func (f *workbenchFixture) spacePG() db.UUID   { return PGUUID(string(f.space)) }
func (f *workbenchFixture) privatePG() db.UUID { return PGUUID(string(f.private)) }
func (f *workbenchFixture) catalogPG() db.UUID { return PGUUID(string(f.catalog)) }
func (f *workbenchFixture) bindingPG() db.UUID { return PGUUID(string(f.binding)) }

// TestSemanticDocsRoundTrip covers the doc reads, writes, search and tags over
// the GORM store (#116, #772).
func TestSemanticDocsRoundTrip(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()
	column := "order_note"
	tableKey := docs.Key{WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Table: "orders"}

	if _, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Description: "Orders, one row per order.", Tags: []string{"finance"}, UpdatedBy: f.ownerPG(), Certified: true,
	}); err != nil {
		t.Fatalf("UpsertSemanticDoc(table): %v", err)
	}
	if _, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		ColumnName: &column, Description: "A note.", UpdatedBy: f.ownerPG(),
	}); err != nil {
		t.Fatalf("UpsertSemanticDoc(column): %v", err)
	}

	got, err := f.r.GetSemanticDoc(ctx, db.GetSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
	})
	if err != nil || !got.Certified || !got.CertifiedBy.Valid || !got.CertifiedAt.Valid {
		t.Fatalf("GetSemanticDoc = %+v, %v", got, err)
	}

	list, err := f.r.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
	})
	if err != nil || len(list) != 2 || list[0].ColumnName != nil || list[1].ColumnName == nil {
		t.Fatalf("ListSemanticDocsForTable = %+v, %v; want the table doc first", list, err)
	}

	// Recertifying keeps the first certification; uncertifying clears it.
	recertified, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Description: "Orders, one row per order.", Tags: []string{"finance"}, UpdatedBy: f.memberPG(), Certified: true,
	})
	if err != nil || !recertified.CertifiedBy.Valid || recertified.CertifiedBy != got.CertifiedBy {
		t.Fatalf("recertify = %+v, %v; want the original certifier", recertified, err)
	}
	uncertified, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Description: "Orders.", Tags: []string{"finance"}, UpdatedBy: f.memberPG(), Certified: false,
	})
	if err != nil || uncertified.Certified || uncertified.CertifiedBy.Valid || uncertified.CertifiedAt.Valid {
		t.Fatalf("uncertify = %+v, %v", uncertified, err)
	}
	hits, err := f.r.SearchSemanticDocs(ctx, db.SearchSemanticDocsParams{WorkspaceID: f.wsPG(), Q: "orders", Lim: 10})
	if err != nil || len(hits) != 2 {
		t.Fatalf("SearchSemanticDocs = %+v, %v", hits, err)
	}
	tableHit := false
	for _, hit := range hits {
		if hit.TableName == "orders" && hit.ColumnName == nil && hit.Score > 0 {
			tableHit = true
		}
	}
	if !tableHit {
		t.Fatalf("SearchSemanticDocs = %+v; want the table doc", hits)
	}
	if hits, err := f.r.SearchSemanticDocs(ctx, db.SearchSemanticDocsParams{WorkspaceID: f.wsPG(), Q: "orders", Tag: strPtr("finance"), Lim: 10}); err != nil || len(hits) != 1 {
		t.Fatalf("SearchSemanticDocs(tag) = %+v, %v", hits, err)
	}
	tags, err := f.r.ListSemanticTags(ctx, db.ListSemanticTagsParams{WorkspaceID: f.wsPG(), Q: "fin", Lim: 10})
	if err != nil || len(tags) != 1 || tags[0].Tag != "finance" || tags[0].Count != 1 {
		t.Fatalf("ListSemanticTags = %+v, %v", tags, err)
	}
	if _, _, err := docs.New(f.r).Upsert(ctx, tableKey, docs.Edit{Description: "Orders, one row per order.", Certified: boolPtr(true)}, f.ownerPG(), true); err != nil {
		t.Fatalf("docs.New(f.r).Upsert: %v", err)
	}

	if _, err := f.r.DeleteSemanticDoc(ctx, db.DeleteSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders", ColumnName: &column,
	}); err != nil {
		t.Fatalf("DeleteSemanticDoc: %v", err)
	}
	if _, err := f.r.GetSemanticDoc(ctx, db.GetSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders", ColumnName: &column,
	}); err == nil {
		t.Fatal("deleted doc still reads")
	}
}

// TestVisibleSemanticMetricsAndModels covers the space-role and catalog-grant
// visibility rules, the version reads and certification (#40, #772).
func TestVisibleSemanticMetricsAndModels(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	metric := func(space db.UUID, slug, label string) db.SemanticMetric {
		t.Helper()
		m, err := f.r.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
			WorkspaceID: f.wsPG(), SpaceID: space, Slug: slug, Label: label,
			CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
			Definition: []byte(`{"measure":{}}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
		})
		if err != nil {
			t.Fatalf("CreateSemanticMetric(%s): %v", slug, err)
		}
		return m
	}
	mDefault := metric(f.spacePG(), "orders_count", "Orders")
	mPrivate := metric(f.privatePG(), "secret_count", "Secret")

	model := func(space db.UUID, slug, label string) db.SemanticModel {
		t.Helper()
		m, err := f.r.CreateSemanticModel(ctx, db.CreateSemanticModelParams{
			WorkspaceID: f.wsPG(), SpaceID: space, Slug: slug, Label: label,
			CatalogID: f.catalogPG(), Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
		})
		if err != nil {
			t.Fatalf("CreateSemanticModel(%s): %v", slug, err)
		}
		return m
	}
	modelDefault := model(f.spacePG(), "orders_model", "Orders model")
	modelPrivate := model(f.privatePG(), "secret_model", "Secret model")

	for i, entity := range []struct {
		kind string
		id   db.UUID
	}{
		{"semantic_metric", mDefault.ID},
		{"semantic_metric", mPrivate.ID},
		{"semantic_model", modelDefault.ID},
		{"semantic_model", modelPrivate.ID},
	} {
		if _, err := f.r.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: f.wsPG(), SpaceID: f.spacePG(), EntityType: entity.kind, EntityID: entity.id,
			Version: int32(i + 1), ChangeKind: "create", Snapshot: []byte(`{}`), AuthorID: f.ownerPG(),
		}); err != nil {
			t.Fatalf("InsertContentVersion: %v", err)
		}
	}
	if versions, err := f.r.ListSemanticMetricVersions(ctx, f.wsPG()); err != nil || len(versions) != 2 {
		t.Fatalf("ListSemanticMetricVersions = %+v, %v", versions, err)
	}
	if versions, err := f.r.ListSemanticModelVersions(ctx, f.wsPG()); err != nil || len(versions) != 2 {
		t.Fatalf("ListSemanticModelVersions = %+v, %v", versions, err)
	}

	memberMetrics, err := f.r.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: f.wsPG(), UserID: f.memberPG()})
	if err != nil || len(memberMetrics) != 1 || memberMetrics[0].ID != mDefault.ID {
		t.Fatalf("member metrics = %+v, %v", memberMetrics, err)
	}
	ownerMetrics, err := f.r.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: f.wsPG(), UserID: f.ownerPG()})
	if err != nil || len(ownerMetrics) != 2 {
		t.Fatalf("owner metrics = %+v, %v", ownerMetrics, err)
	}
	if narrowed, err := f.r.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{
		WorkspaceID: f.wsPG(), UserID: f.ownerPG(), SpaceID: f.privatePG(),
	}); err != nil || len(narrowed) != 1 || narrowed[0].ID != mPrivate.ID {
		t.Fatalf("owner private metrics = %+v, %v", narrowed, err)
	}

	memberModels, err := f.r.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: f.wsPG(), UserID: f.memberPG()})
	if err != nil || len(memberModels) != 1 || memberModels[0].ID != modelDefault.ID {
		t.Fatalf("member models = %+v, %v", memberModels, err)
	}
	ownerModels, err := f.r.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: f.wsPG(), UserID: f.ownerPG()})
	if err != nil || len(ownerModels) != 2 {
		t.Fatalf("owner models = %+v, %v", ownerModels, err)
	}

	certified, err := f.r.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
		WorkspaceID: f.wsPG(), ID: mDefault.ID, Certified: true, Actor: f.ownerPG(),
	})
	if err != nil || !certified.Certified || !certified.CertifiedBy.Valid || !certified.CertifiedAt.Valid {
		t.Fatalf("SetSemanticMetricCertification = %+v, %v", certified, err)
	}
	cleared, err := f.r.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
		WorkspaceID: f.wsPG(), ID: mDefault.ID, Certified: false, Actor: f.ownerPG(),
	})
	if err != nil || cleared.Certified || cleared.CertifiedBy.Valid || cleared.CertifiedAt.Valid {
		t.Fatalf("uncertified metric = %+v, %v", cleared, err)
	}
	modelCertified, err := f.r.SetSemanticModelCertification(ctx, db.SetSemanticModelCertificationParams{
		WorkspaceID: f.wsPG(), ID: modelDefault.ID, Certified: true, Actor: f.ownerPG(),
	})
	if err != nil || !modelCertified.Certified || !modelCertified.CertifiedBy.Valid {
		t.Fatalf("SetSemanticModelCertification = %+v, %v", modelCertified, err)
	}
}

// TestSemanticSearchService covers the content search over the GORM store
// (#131, #772).
func TestSemanticSearchService(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	folder, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsPG(), SpaceID: f.spacePG(), Name: "Reports", Slug: "reports",
	})
	if err != nil {
		t.Fatalf("CreateQueryFolder: %v", err)
	}
	if _, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: f.wsPG(), SpaceID: f.spacePG(), FolderID: folder.ID, CreatedBy: f.ownerPG(),
		UpdatedBy: f.ownerPG(), Name: "Monthly revenue", Slug: "monthly_revenue", Sql: "select 1",
	}); err != nil {
		t.Fatalf("CreateSavedQuery: %v", err)
	}
	if _, err := f.r.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
		WorkspaceID: f.wsPG(), SpaceID: f.spacePG(), Slug: "revenue", Label: "Revenue",
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
	}); err != nil {
		t.Fatalf("CreateSemanticMetric: %v", err)
	}
	if _, err := f.r.CreateSemanticModel(ctx, db.CreateSemanticModelParams{
		WorkspaceID: f.wsPG(), SpaceID: f.spacePG(), Slug: "revenue_model", Label: "Revenue model",
		CatalogID: f.catalogPG(), Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
	}); err != nil {
		t.Fatalf("CreateSemanticModel: %v", err)
	}
	if _, err := f.r.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
		WorkspaceID: f.wsPG(), SpaceID: f.privatePG(), Slug: "secret_revenue", Label: "Secret revenue",
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
	}); err != nil {
		t.Fatalf("CreateSemanticMetric(private): %v", err)
	}

	svc := search.New(f.r)
	memberHits, err := svc.Search(ctx, f.wsPG(), f.memberPG(), search.Query{Text: "revenue"})
	if err != nil || len(memberHits) != 3 {
		t.Fatalf("member search = %+v, %v; want folder? no: saved query, metric and model", memberHits, err)
	}
	kinds := map[string]int{}
	for _, hit := range memberHits {
		kinds[hit.Kind]++
		if hit.Title == "Secret revenue" {
			t.Fatalf("member search leaked the private metric: %+v", hit)
		}
	}
	if kinds[search.KindSavedQuery] != 1 || kinds[search.KindSemanticMetric] != 1 || kinds[search.KindSemanticModel] != 1 {
		t.Fatalf("member search kinds = %+v", kinds)
	}
	ownerHits, err := svc.Search(ctx, f.wsPG(), f.ownerPG(), search.Query{Text: "revenue"})
	if err != nil || len(ownerHits) != 4 {
		t.Fatalf("owner search = %+v, %v", ownerHits, err)
	}
}

// TestMetadataBrowse covers namespaces, tables, columns, foreign keys, sync
// runs, the binding display and the metadata search (#18, #772).
func TestMetadataBrowse(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	for _, ns := range [][]string{{"sales"}, {"ops"}, {"ops", "b.c"}} {
		if err := f.r.UpsertMetadataNamespace(ctx, db.UpsertMetadataNamespaceParams{CatalogID: f.catalogPG(), Namespace: ns}); err != nil {
			t.Fatalf("UpsertMetadataNamespace(%v): %v", ns, err)
		}
	}
	run, err := f.r.StartMetadataSyncRun(ctx, db.StartMetadataSyncRunParams{CatalogID: f.catalogPG(), SourcePath: "provider"})
	if err != nil {
		t.Fatalf("StartMetadataSyncRun: %v", err)
	}
	if err := f.r.FinishMetadataSyncRun(ctx, db.FinishMetadataSyncRunParams{
		ID: run.ID, Status: "finished", TablesSeen: i32(2), TablesChanged: i32(2), TablesRemoved: i32(0),
	}); err != nil {
		t.Fatalf("FinishMetadataSyncRun: %v", err)
	}

	table := func(namespace []string, name string) db.UpsertMetadataTableRow {
		t.Helper()
		row, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
			CatalogID: f.catalogPG(), Namespace: namespace, Name: name, Kind: "table",
			Fingerprint: "fp-" + name, Properties: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("UpsertMetadataTable(%s): %v", name, err)
		}
		return row
	}
	orders := table([]string{"sales"}, "orders")
	history := table([]string{"sales"}, "customer_order_history")
	deep := table([]string{"ops", "b.c"}, "deep")
	if !orders.Changed || !history.Changed || !deep.Changed {
		t.Fatalf("new tables must be changed: %+v %+v %+v", orders, history, deep)
	}
	again, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "orders", Kind: "table",
		Fingerprint: "fp-orders", Properties: []byte(`{}`),
	})
	if err != nil || again.Changed || again.ID != orders.ID {
		t.Fatalf("unchanged upsert = %+v, %v", again, err)
	}
	if n, err := f.r.TouchMetadataTable(ctx, db.TouchMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "orders",
	}); err != nil || n != 1 {
		t.Fatalf("TouchMetadataTable = %d, %v", n, err)
	}

	if err := f.r.InsertMetadataColumns(ctx, db.InsertMetadataColumnsParams{
		TableID: orders.ID, TypeSystem: "iceberg",
		Ordinals: []int32{1, 2, 3}, Names: []string{"id", "tags", "order_note"},
		NativeTypes: []string{"long", "list<string>", "string"}, Nullables: []bool{false, true, true},
		Comments: []string{"", "", "a note"},
	}); err != nil {
		t.Fatalf("InsertMetadataColumns: %v", err)
	}
	columns, err := f.r.ListMetadataColumns(ctx, orders.ID)
	if err != nil || len(columns) != 3 || columns[0].Name != "id" || columns[2].Comment == nil {
		t.Fatalf("ListMetadataColumns = %+v, %v", columns, err)
	}
	if err := f.r.InsertMetadataForeignKeys(ctx, db.InsertMetadataForeignKeysParams{
		CatalogID: f.catalogPG(), TableID: orders.ID,
		ForeignKeys: []byte(`[{"column_name":"id","ref_namespace":["ops","b.c"],"ref_table":"deep","ref_column":"id"}]`),
	}); err != nil {
		t.Fatalf("InsertMetadataForeignKeys: %v", err)
	}
	var fkCount int64
	if err := f.gdb.Model(&model.MetadataForeignKey{}).Where("table_id = ?", UUIDString(orders.ID)).
		Count(&fkCount).Error; err != nil || fkCount != 1 {
		t.Fatalf("foreign keys = %d, %v", fkCount, err)
	}
	if err := f.r.DeleteMetadataForeignKeys(ctx, orders.ID); err != nil {
		t.Fatalf("DeleteMetadataForeignKeys: %v", err)
	}

	catalogs, err := f.r.ListMetadataCatalogs(ctx, f.wsPG())
	if err != nil || len(catalogs) != 1 || catalogs[0].MetadataSyncStatus != "finished" || !catalogs[0].MetadataSyncedAt.Valid ||
		catalogs[0].ProviderKind != "polaris" || len(catalogs[0].Bindings) == 0 {
		t.Fatalf("ListMetadataCatalogs = %+v, %v", catalogs, err)
	}
	if catalog, err := f.r.GetGrantedMetadataCatalog(ctx, db.GetGrantedMetadataCatalogParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(),
	}); err != nil || catalog.ID != f.catalogPG() || !catalog.MetadataSyncedAt.Valid {
		t.Fatalf("GetGrantedMetadataCatalog = %+v, %v", catalog, err)
	}
	if _, err := f.r.GetGrantedMetadataCatalog(ctx, db.GetGrantedMetadataCatalogParams{
		WorkspaceID: f.wsPG(), CatalogID: PGUUID(NewUUID()),
	}); err == nil {
		t.Fatal("an ungranted catalog reads")
	}

	top, err := f.r.ListChildNamespaces(ctx, db.ListChildNamespacesParams{CatalogID: f.catalogPG(), Parent: []string{}})
	if err != nil || len(top) != 2 || top[0][0] != "ops" || top[1][0] != "sales" {
		t.Fatalf("top namespaces = %v, %v", top, err)
	}
	child, err := f.r.ListChildNamespaces(ctx, db.ListChildNamespacesParams{CatalogID: f.catalogPG(), Parent: []string{"ops"}})
	if err != nil || len(child) != 1 || len(child[0]) != 2 || child[0][1] != "b.c" {
		t.Fatalf("children of ops = %v, %v", child, err)
	}
	page, err := f.r.ListMetadataTablesPage(ctx, db.ListMetadataTablesPageParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, After: "", Lim: 10,
	})
	if err != nil || len(page) != 2 || page[0].Name != "customer_order_history" || page[1].Name != "orders" {
		t.Fatalf("tables page = %+v, %v", page, err)
	}
	granted, err := f.r.GetGrantedMetadataTable(ctx, db.GetGrantedMetadataTableParams{WorkspaceID: f.wsPG(), TableID: orders.ID})
	if err != nil || granted.Name != "orders" || !granted.MetadataSyncedAt.Valid {
		t.Fatalf("GetGrantedMetadataTable = %+v, %v", granted, err)
	}
	binding, err := f.r.GetCatalogBindingForDisplay(ctx, db.GetCatalogBindingForDisplayParams{CatalogID: f.catalogPG()})
	if err != nil || binding.ID != f.bindingPG() || binding.EngineKind != "trino" {
		t.Fatalf("GetCatalogBindingForDisplay = %+v, %v", binding, err)
	}
	runs, err := f.r.ListMetadataSyncRuns(ctx, db.ListMetadataSyncRunsParams{CatalogID: f.catalogPG(), Limit: 10})
	if err != nil || len(runs) != 1 || runs[0].Status != "finished" || !runs[0].FinishedAt.Valid {
		t.Fatalf("ListMetadataSyncRuns = %+v, %v", runs, err)
	}
	fingerprints, err := f.r.GetTableFingerprints(ctx, f.catalogPG())
	if err != nil || len(fingerprints) != 3 {
		t.Fatalf("GetTableFingerprints = %+v, %v", fingerprints, err)
	}

	if _, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
		Description: "", Tags: []string{"finance"}, UpdatedBy: f.ownerPG(), Certified: true,
	}); err != nil {
		t.Fatalf("UpsertSemanticDoc: %v", err)
	}
	hits, err := f.r.SearchMetadata(ctx, db.SearchMetadataParams{
		WorkspaceID: f.wsPG(), Q: "ord", Kind: "all", Lim: 50,
	})
	if err != nil || len(hits) == 0 {
		t.Fatalf("SearchMetadata = %+v, %v", hits, err)
	}
	var sawOrders, sawHistory, sawColumn bool
	for _, hit := range hits {
		if hit.Kind == "table" && hit.TableName == "orders" {
			sawOrders = true
		}
		if hit.Kind == "table" && hit.TableName == "customer_order_history" {
			sawHistory = true
		}
		if hit.Kind == "column" && hit.ColumnName == "order_note" {
			sawColumn = true
		}
	}
	if !sawOrders || !sawHistory || !sawColumn {
		t.Fatalf("search hits = %+v; want orders, customer_order_history and order_note", hits)
	}
	filtered, err := f.r.SearchMetadata(ctx, db.SearchMetadataParams{
		WorkspaceID: f.wsPG(), Q: "order", Kind: "all", Tags: []string{"finance"}, Lim: 50,
	})
	if err != nil || len(filtered) == 0 {
		t.Fatalf("SearchMetadata(tag) = %+v, %v", filtered, err)
	}
	for _, hit := range filtered {
		if hit.TableName != "orders" {
			t.Fatalf("tag filter returned %+v", hit)
		}
	}
	if none, err := f.r.SearchMetadata(ctx, db.SearchMetadataParams{
		WorkspaceID: f.wsPG(), Q: "order", Kind: "all", Namespace: []string{"ops"}, Lim: 50,
	}); err != nil || len(none) != 0 {
		t.Fatalf("namespace filter = %+v, %v", none, err)
	}

	// Tombstoning removes the tables from the page and the search.
	passStart := db.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true}
	if n, err := f.r.TombstoneMissingTables(ctx, db.TombstoneMissingTablesParams{CatalogID: f.catalogPG(), PassStartedAt: passStart}); err != nil || n != 3 {
		t.Fatalf("TombstoneMissingTables = %d, %v", n, err)
	}
	if err := f.r.TombstoneMissingNamespaces(ctx, db.TombstoneMissingNamespacesParams{CatalogID: f.catalogPG(), PassStartedAt: passStart}); err != nil {
		t.Fatalf("TombstoneMissingNamespaces: %v", err)
	}
	if page, err := f.r.ListMetadataTablesPage(ctx, db.ListMetadataTablesPageParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Lim: 10,
	}); err != nil || len(page) != 0 {
		t.Fatalf("tombstoned page = %+v, %v", page, err)
	}
	if none, err := f.r.SearchMetadata(ctx, db.SearchMetadataParams{
		WorkspaceID: f.wsPG(), Q: "orders", Kind: "all", Lim: 50,
	}); err != nil || len(none) != 0 {
		t.Fatalf("tombstoned search = %+v, %v", none, err)
	}
}

func i32(v int32) *int32 { return &v }

// TestPopularTables covers the read and the GORM refresh of the popularity
// snapshot (#562, #772).
func TestPopularTables(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	table, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "orders", Kind: "table",
		Fingerprint: "fp", Properties: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("UpsertMetadataTable: %v", err)
	}
	if _, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "other", Kind: "table",
		Fingerprint: "fp2", Properties: []byte(`{}`),
	}); err != nil {
		t.Fatalf("UpsertMetadataTable(other): %v", err)
	}

	exec, err := f.r.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID: f.wsPG(), UserID: f.ownerPG(), Status: "finished", Sql: "select 1",
	})
	if err != nil {
		t.Fatalf("CreateQueryExecution: %v", err)
	}
	stmt, err := f.r.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
		ExecutionID: exec.ID, Position: 1, Sql: "select 1", Status: "finished",
	})
	if err != nil {
		t.Fatalf("CreateQueryStatement: %v", err)
	}
	for _, alias := range []string{"o", "o2"} {
		if err := f.r.InsertStatementReference(ctx, db.InsertStatementReferenceParams{
			StatementID: stmt.ID, WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(),
			Namespace: "sales", TableName: "orders", Alias: alias, Kind: "read",
		}); err != nil {
			t.Fatalf("InsertStatementReference: %v", err)
		}
	}
	if err := f.r.RefreshTablePopularity(ctx); err != nil {
		t.Fatalf("RefreshTablePopularity: %v", err)
	}
	rows, err := f.r.ListPopularTables(ctx, db.ListPopularTablesParams{WorkspaceID: f.wsPG(), Lim: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListPopularTables = %+v, %v", rows, err)
	}
	if rows[0].TableID != table.ID || rows[0].Uses != 2 || rows[0].Score <= 0 || !rows[0].LastUsedAt.Valid {
		t.Fatalf("popular row = %+v; want orders used twice", rows[0])
	}

	// A second read updates the count; an ungranted catalog never appears.
	if err := f.gdb.Model(&model.Catalog{}).Where("id = ?", string(f.catalog)).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable catalog: %v", err)
	}
	rows, err = f.r.ListPopularTables(ctx, db.ListPopularTablesParams{WorkspaceID: f.wsPG(), Lim: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("ungranted popular rows = %+v, %v", rows, err)
	}
}

// TestLineage covers the lineage reads and the broken-reference candidates,
// including a user with and without the effective space role (#558, #772).
func TestLineage(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	table, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "orders", Kind: "table",
		Fingerprint: "fp", Properties: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("UpsertMetadataTable: %v", err)
	}

	metric := func(space db.UUID, slug string) db.SemanticMetric {
		t.Helper()
		m, err := f.r.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
			WorkspaceID: f.wsPG(), SpaceID: space, Slug: slug, Label: slug,
			CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "orders",
			Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
		})
		if err != nil {
			t.Fatalf("CreateSemanticMetric(%s): %v", slug, err)
		}
		return m
	}
	model := func(space db.UUID, slug string) db.SemanticModel {
		t.Helper()
		m, err := f.r.CreateSemanticModel(ctx, db.CreateSemanticModelParams{
			WorkspaceID: f.wsPG(), SpaceID: space, Slug: slug, Label: slug,
			CatalogID: f.catalogPG(), Definition: []byte(`{}`), FormatVersion: 1, UpdatedBy: f.ownerPG(),
		})
		if err != nil {
			t.Fatalf("CreateSemanticModel(%s): %v", slug, err)
		}
		return m
	}
	publicMetric := metric(f.spacePG(), "public_metric")
	privateMetric := metric(f.privatePG(), "private_metric")
	publicModel := model(f.spacePG(), "public_model")
	privateModel := model(f.privatePG(), "private_model")

	query := func(space db.UUID, slug string) db.SavedQuery {
		t.Helper()
		q, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
			WorkspaceID: f.wsPG(), SpaceID: space, CreatedBy: f.ownerPG(), UpdatedBy: f.ownerPG(),
			Name: slug, Slug: slug, Sql: "select * from sales.orders", TargetBindingID: f.bindingPG(),
		})
		if err != nil {
			t.Fatalf("CreateSavedQuery(%s): %v", slug, err)
		}
		return q
	}
	publicQuery := query(f.spacePG(), "public_query")
	query(f.privatePG(), "private_query")

	ref, err := f.r.GetMetadataTableByRef(ctx, db.GetMetadataTableByRefParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "orders",
	})
	if err != nil || ref.ID != table.ID {
		t.Fatalf("GetMetadataTableByRef = %+v, %v", ref, err)
	}
	if _, err := f.r.GetMetadataTableByRef(ctx, db.GetMetadataTableByRefParams{
		CatalogID: f.catalogPG(), Namespace: []string{"sales"}, Name: "missing",
	}); err == nil {
		t.Fatal("a missing table resolves")
	}

	memberModels, err := f.r.ListLineageModels(ctx, db.ListLineageModelsParams{WorkspaceID: f.wsPG(), UserID: f.memberPG()})
	if err != nil || len(memberModels) != 1 || memberModels[0].ID != publicModel.ID {
		t.Fatalf("member lineage models = %+v, %v", memberModels, err)
	}
	ownerModels, err := f.r.ListLineageModels(ctx, db.ListLineageModelsParams{WorkspaceID: f.wsPG(), UserID: f.ownerPG()})
	if err != nil || len(ownerModels) != 2 {
		t.Fatalf("owner lineage models = %+v, %v", ownerModels, err)
	}
	memberMetrics, err := f.r.ListLineageMetrics(ctx, db.ListLineageMetricsParams{WorkspaceID: f.wsPG(), UserID: f.memberPG()})
	if err != nil || len(memberMetrics) != 1 || memberMetrics[0].ID != publicMetric.ID {
		t.Fatalf("member lineage metrics = %+v, %v", memberMetrics, err)
	}
	ownerMetrics, err := f.r.ListLineageMetrics(ctx, db.ListLineageMetricsParams{WorkspaceID: f.wsPG(), UserID: f.ownerPG()})
	if err != nil || len(ownerMetrics) != 2 {
		t.Fatalf("owner lineage metrics = %+v, %v", ownerMetrics, err)
	}
	memberQueries, err := f.r.ListLineageSavedQueries(ctx, db.ListLineageSavedQueriesParams{WorkspaceID: f.wsPG(), UserID: f.memberPG()})
	if err != nil || len(memberQueries) != 1 || memberQueries[0].ID != publicQuery.ID || memberQueries[0].TargetCatalogID != f.catalogPG() {
		t.Fatalf("member lineage queries = %+v, %v", memberQueries, err)
	}
	ownerQueries, err := f.r.ListLineageSavedQueries(ctx, db.ListLineageSavedQueriesParams{WorkspaceID: f.wsPG(), UserID: f.ownerPG()})
	if err != nil || len(ownerQueries) != 2 {
		t.Fatalf("owner lineage queries = %+v, %v", ownerQueries, err)
	}

	brokenModels, err := f.r.ListLineageBrokenModels(ctx)
	if err != nil || len(brokenModels) != 2 {
		t.Fatalf("ListLineageBrokenModels = %+v, %v", brokenModels, err)
	}
	brokenMetrics, err := f.r.ListLineageBrokenMetrics(ctx)
	if err != nil || len(brokenMetrics) != 2 {
		t.Fatalf("ListLineageBrokenMetrics = %+v, %v", brokenMetrics, err)
	}
	for _, row := range brokenMetrics {
		if row.ID != publicMetric.ID && row.ID != privateMetric.ID {
			t.Fatalf("broken metric %+v", row)
		}
	}
	for _, row := range brokenModels {
		if row.ID != publicModel.ID && row.ID != privateModel.ID {
			t.Fatalf("broken model %+v", row)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
