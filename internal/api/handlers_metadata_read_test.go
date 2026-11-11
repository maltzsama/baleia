package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"gorm.io/gorm"
)

// metaFixture is a database with two users, each with a personal workspace;
// the first workspace is granted catalog "lake" (Iceberg, bound into Trino),
// not catalog "other", and its owner owns catalog "mine".
type metaFixture struct {
	pool            *gorm.DB
	gdb             *gorm.DB
	h               *API
	owner, stranger string
	ws, strangerWS  openapi_types.UUID
	lake, other     openapi_types.UUID
	mine            openapi_types.UUID // on the owner's own connection, not granted
	binding         openapi_types.UUID
	orders, removed openapi_types.UUID
	otherTable      openapi_types.UUID
}

func newMetaFixture(t *testing.T) *metaFixture {
	t.Helper()
	pool := testdb.New(t)
	gdb := pool
	f := &metaFixture{pool: pool, gdb: gdb}
	q := repo.New(gdb)
	create := func(model any) {
		t.Helper()
		if err := gdb.Create(model).Error; err != nil {
			t.Fatalf("create %T: %v", model, err)
		}
	}
	openapiID := func(id model.UUID) openapi_types.UUID {
		return openapi_types.UUID(repo.PGUUID(string(id)).Bytes)
	}
	user := func(name, email string) model.UUID {
		password := "x"
		row := model.User{
			ID: model.UUID(repo.NewUUID()), Name: name, Email: model.Text(email), PasswordHash: &password,
		}
		create(&row)
		return row.ID
	}
	ownerID, strangerID := user("o", "o@x"), user("s", "s@x")
	f.owner, f.stranger = string(ownerID), string(strangerID)
	workspace := func(name, polaris string, owner model.UUID) model.Workspace {
		row := model.Workspace{
			ID: model.UUID(repo.NewUUID()), Name: name, Type: "personal",
			PolarisCatalogName: model.Text(polaris), OwnerUserID: &owner,
		}
		create(&row)
		return row
	}
	ws, strangerWS := workspace("w", "p", ownerID), workspace("w2", "p2", strangerID)
	f.ws, f.strangerWS = openapiID(ws.ID), openapiID(strangerWS.ID)
	addDefaultSpace(t, gdb, ws.ID)
	addDefaultSpace(t, gdb, strangerWS.ID)

	connection := func(name, kind, url string, createdBy *model.UUID) model.UUID {
		row := model.CatalogConnection{
			ID: model.UUID(repo.NewUUID()), Name: model.Text(name), Type: kind, URL: url, CreatedBy: createdBy,
		}
		create(&row)
		return row.ID
	}
	conn := connection("pol", "polaris", "http://p", nil)
	trinoConn := connection("tri", "trino", "http://t", nil)
	engine, err := q.CreateEngine(context.Background(), db.CreateEngineParams{
		Name: "trino", Kind: "trino", Class: "federated", ConnectionID: repo.PGUUID(string(trinoConn)),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := func(conn model.UUID, name string) model.UUID {
		row := model.Catalog{
			ID: model.UUID(repo.NewUUID()), ConnectionID: conn, Name: model.Text(name), Origin: "native",
		}
		create(&row)
		return row.ID
	}
	lake, other := catalog(conn, "lake"), catalog(conn, "other")
	ownConn := connection("own", "postgresql", "postgres://x", &ownerID)
	mine := catalog(ownConn, "mine")
	f.lake, f.other, f.mine = openapiID(lake), openapiID(other), openapiID(mine)
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: lake, EngineID: model.UUID(engine.ID.String()),
		EngineCatalogName: "lake_t", Mode: "adopted",
	}
	create(&binding)
	f.binding = openapiID(binding.ID)
	if _, err := q.UpsertWorkspaceCatalogGrant(context.Background(), db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: repo.PGUUID(string(ws.ID)), CatalogID: repo.PGUUID(string(lake)), GrantedBy: repo.PGUUID(string(ownerID)),
	}); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Now()
	create(&model.MetadataSyncRun{
		ID: model.UUID(repo.NewUUID()), CatalogID: lake, Status: "finished",
		SourcePath: "provider", FinishedAt: &finishedAt,
	})

	for _, ns := range []model.StringArray{{"sales"}, {"ops"}, {"ops", "b.c"}} {
		create(&model.MetadataNamespace{CatalogID: lake, Namespace: ns, LastSeenAt: time.Now()})
	}
	table := func(cat model.UUID, ns model.StringArray, name string, removed bool) model.UUID {
		t.Helper()
		row := model.MetadataTable{
			ID: model.UUID(repo.NewUUID()), CatalogID: cat, Namespace: ns, Name: model.Text(name),
			Kind: "table", Fingerprint: "f", LastSeenAt: time.Now(), LastChangedAt: time.Now(),
		}
		if removed {
			at := time.Now()
			row.RemovedAt = &at
		}
		create(&row)
		return row.ID
	}
	orders := table(lake, model.StringArray{"sales"}, "orders", false)
	f.orders = openapiID(orders)
	table(lake, model.StringArray{"sales"}, "customer_order_history", false)
	f.removed = openapiID(table(lake, model.StringArray{"sales"}, "orders_old", true))
	f.otherTable = openapiID(table(other, model.StringArray{"sales"}, "orders_secret", false))
	table(lake, model.StringArray{"ops", "b.c"}, "deep", false)

	nullable := func(v bool) *bool { return &v }
	for _, col := range []struct {
		ordinal  int32
		name     model.Text
		typ      string
		nullable *bool
	}{
		{1, "id", "long", nullable(false)},
		{2, "tags", "list<string>", nullable(true)},
		{3, "order_note", "string", nullable(true)},
	} {
		create(&model.MetadataColumn{
			TableID: orders, Ordinal: col.ordinal, Name: col.name, NativeType: col.typ,
			TypeSystem: "iceberg", Nullable: col.nullable,
		})
	}

	f.h = testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: q, store: gdb, tx: storeTx{db: pool}}) // no engines: reads never call one
	return f
}

func (f *metaFixture) as(user string) context.Context { return ctxAs(user, "member") }

func TestMetadata_ForeignWorkspace404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.stranger)
	r1, _ := f.h.ListMetadataCatalogs(ctx, ListMetadataCatalogsRequestObject{Id: f.ws})
	r2, _ := f.h.ListMetadataNamespaces(ctx, ListMetadataNamespacesRequestObject{Id: f.ws, CatalogId: f.lake})
	r3, _ := f.h.ListMetadataTables(ctx, ListMetadataTablesRequestObject{Id: f.ws, CatalogId: f.lake})
	r4, _ := f.h.GetMetadataTable(ctx, GetMetadataTableRequestObject{Id: f.ws, TableId: f.orders})
	r5, _ := f.h.SearchMetadata(ctx, SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "ord"}})
	for i, r := range []any{r1, r2, r3, r4, r5} {
		switch r.(type) {
		case ListMetadataCatalogs404JSONResponse, ListMetadataNamespaces404JSONResponse, ListMetadataTables404JSONResponse,
			GetMetadataTable404JSONResponse, SearchMetadata404JSONResponse:
		default:
			t.Errorf("path %d: %T, want 404", i+1, r)
		}
	}
}

// TestMetadata_UngrantedCatalog404: a catalog not granted to the workspace is
// invisible: 404 on its namespaces, tables and table, and search returns
// nothing from it.
func TestMetadata_UngrantedCatalog404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	if r, _ := f.h.ListMetadataNamespaces(ctx, ListMetadataNamespacesRequestObject{Id: f.ws, CatalogId: f.other}); !is404(r) {
		t.Errorf("namespaces: %T", r)
	}
	if r, _ := f.h.ListMetadataTables(ctx, ListMetadataTablesRequestObject{Id: f.ws, CatalogId: f.other}); !is404(r) {
		t.Errorf("tables: %T", r)
	}
	if r, _ := f.h.GetMetadataTable(ctx, GetMetadataTableRequestObject{Id: f.ws, TableId: f.otherTable}); !is404(r) {
		t.Errorf("table: %T", r)
	}
	r, err := f.h.SearchMetadata(ctx, SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "orders_secret"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range r.(SearchMetadata200JSONResponse) {
		if hit.CatalogId == f.other {
			t.Fatalf("search leaked an ungranted catalog: %+v", hit)
		}
	}
	cats, err := f.h.ListMetadataCatalogs(ctx, ListMetadataCatalogsRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	list := cats.(ListMetadataCatalogs200JSONResponse)
	// Only the granted catalog is visible: the owner's own, ungranted catalog
	// no longer auto-reaches (ADR 0018 §4).
	if len(list) != 1 {
		t.Fatalf("catalogs = %+v", list)
	}
	if list[0].Id != f.lake || list[0].MetadataSyncStatus != "finished" || list[0].MetadataSyncedAt == nil ||
		len(list[0].Bindings) != 1 || list[0].Bindings[0].BindingId != f.binding || list[0].Bindings[0].EngineKind != "trino" || list[0].ProviderKind != "polaris" {
		t.Fatalf("catalogs = %+v", list)
	}
}

func is404(r any) bool {
	switch r.(type) {
	case ListMetadataNamespaces404JSONResponse, ListMetadataTables404JSONResponse, GetMetadataTable404JSONResponse:
		return true
	}
	return false
}

// TestMetadata_MultiLevelNamespace: parent=ops lists ["ops","b.c"]; the dot
// stays inside its level, and ns=ops&ns=b.c addresses that namespace.
func TestMetadata_MultiLevelNamespace(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	top, err := f.h.ListMetadataNamespaces(ctx, ListMetadataNamespacesRequestObject{Id: f.ws, CatalogId: f.lake})
	if err != nil {
		t.Fatal(err)
	}
	if items := top.(ListMetadataNamespaces200JSONResponse).Items; len(items) != 2 || items[0].Path[0] != "ops" || items[1].Path[0] != "sales" {
		t.Fatalf("top = %+v", items)
	}
	parent := []string{"ops"}
	child, err := f.h.ListMetadataNamespaces(ctx, ListMetadataNamespacesRequestObject{Id: f.ws, CatalogId: f.lake, Params: ListMetadataNamespacesParams{Parent: &parent}})
	if err != nil {
		t.Fatal(err)
	}
	items := child.(ListMetadataNamespaces200JSONResponse).Items
	if len(items) != 1 || len(items[0].Path) != 2 || items[0].Path[1] != "b.c" {
		t.Fatalf("children of ops = %+v", items)
	}
	ns := []string{"ops", "b.c"}
	tables, err := f.h.ListMetadataTables(ctx, ListMetadataTablesRequestObject{Id: f.ws, CatalogId: f.lake, Params: ListMetadataTablesParams{Ns: &ns}})
	if err != nil {
		t.Fatal(err)
	}
	if page := tables.(ListMetadataTables200JSONResponse); len(page.Items) != 1 || page.Items[0].Name != "deep" || page.Freshness.MetadataSyncedAt == nil {
		t.Fatalf("tables of ops.b.c = %+v", page)
	}
}

// TestMetadata_Pagination: 250 tables come back in 3 pages, in name order,
// each once; tombstoned tables are not listed.
func TestMetadata_Pagination(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	for i := range 250 {
		row := model.MetadataTable{
			ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(pgUUID(f.lake))),
			Namespace: model.StringArray{"bulk"}, Name: model.Text(fmt.Sprintf("t%03d", 249-i)),
			Kind: "table", Fingerprint: "f", LastSeenAt: time.Now(), LastChangedAt: time.Now(),
		}
		if err := f.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ns := []string{"bulk"}
	seen := map[string]bool{}
	var names []string
	var cursor *string
	pages := 0
	for {
		r, err := f.h.ListMetadataTables(f.as(f.owner), ListMetadataTablesRequestObject{Id: f.ws, CatalogId: f.lake, Params: ListMetadataTablesParams{Ns: &ns, Cursor: cursor}})
		if err != nil {
			t.Fatal(err)
		}
		page := r.(ListMetadataTables200JSONResponse)
		pages++
		for _, it := range page.Items {
			if seen[it.Name] {
				t.Fatalf("duplicate %s", it.Name)
			}
			seen[it.Name] = true
			names = append(names, it.Name)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	if pages != 3 || len(names) != 250 || names[0] != "t000" || names[249] != "t249" {
		t.Fatalf("pages %d, %d names, first %s last %s", pages, len(names), names[0], names[len(names)-1])
	}

	sales := []string{"sales"}
	r, _ := f.h.ListMetadataTables(f.as(f.owner), ListMetadataTablesRequestObject{Id: f.ws, CatalogId: f.lake, Params: ListMetadataTablesParams{Ns: &sales}})
	for _, it := range r.(ListMetadataTables200JSONResponse).Items {
		if it.Name == "orders_old" {
			t.Fatal("a removed table is listed")
		}
	}
}

// TestSearch_Ranking: "ord" ranks orders above customer_order_history, and
// finds columns too.
func TestSearch_Ranking(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	r, err := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "ord"}})
	if err != nil {
		t.Fatal(err)
	}
	hits := r.(SearchMetadata200JSONResponse)
	pos := map[string]int{}
	for i, h := range hits {
		key := h.Table
		if h.Column != nil {
			key += "." + *h.Column
		}
		pos[key] = i + 1
	}
	if pos["orders"] == 0 || pos["customer_order_history"] == 0 || pos["orders"] > pos["customer_order_history"] {
		t.Fatalf("ranking = %v", pos)
	}
	if pos["orders.order_note"] == 0 {
		t.Fatalf("columns are searched too: %v", pos)
	}
	kind := Column
	r, _ = f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "ord", Kind: &kind}})
	for _, h := range r.(SearchMetadata200JSONResponse) {
		if h.Kind != "column" {
			t.Fatalf("kind=column returned %+v", h)
		}
	}
	if r, _ := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "o"}}); func() bool {
		_, ok := r.(SearchMetadata422JSONResponse)
		return !ok
	}() {
		t.Fatal("a one-character query is rejected")
	}
}

// Every search surface counts whether it found anything, for the
// zero-result rate (#941).
func TestSearch_ZeroResultTelemetry(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	reg := metrics.New("api")
	f.h.metrics.search = reg.Search()
	ctx := f.as(f.owner)
	if _, err := f.h.SearchMetadata(ctx, SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "orders"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.SearchMetadata(ctx, SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "zzzzzz"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.SearchContent(ctx, SearchContentRequestObject{Id: f.ws, Params: SearchContentParams{Q: "zzzzzz"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.SearchSemanticDocs(ctx, SearchSemanticDocsRequestObject{Id: f.ws, Params: SearchSemanticDocsParams{Q: "zzzzzz"}}); err != nil {
		t.Fatal(err)
	}
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
	want := map[string]float64{"metadata/hit": 1, "metadata/empty": 1, "content/empty": 1, "docs/empty": 1}
	for key, n := range want {
		if got[key] != n {
			t.Errorf("baleia_search_requests_total %s = %v, want %v (all: %v)", key, got[key], n, got)
		}
	}
}

// A prefix-only search returns the names that start with the text and forgives
// no typo; the default search matches words and misspellings too (#943).
func TestSearch_PrefixOnly(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	search := func(q string, match *SearchMetadataParamsMatch) []string {
		t.Helper()
		r, err := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: q, Match: match}})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, h := range r.(SearchMetadata200JSONResponse) {
			name := h.Table
			if h.Column != nil {
				name = *h.Column
			}
			out = append(out, name)
		}
		return out
	}
	prefix := Prefix

	got := search("ord", &prefix)
	if len(got) == 0 {
		t.Fatal("a prefix finds the names that start with it")
	}
	for _, name := range got {
		if !strings.HasPrefix(name, "ord") {
			t.Errorf("a prefix-only search returned %q, which does not start with ord: %v", name, got)
		}
	}
	if !slices.Contains(got, "orders") || !slices.Contains(got, "order_note") {
		t.Errorf("prefix-only hits = %v, want the table orders and its column order_note", got)
	}
	if def := search("ord", nil); !slices.Contains(def, "customer_order_history") {
		t.Errorf("the default search also matches a word inside a name: %v", def)
	}

	if got := search("ordrs", &prefix); len(got) != 0 {
		t.Errorf("a prefix-only search forgave a typo: %v", got)
	}
	if def := search("ordrs", nil); !slices.Contains(def, "orders") {
		t.Errorf("the default search forgives it: %v", def)
	}

	// A mode that does not exist is refused, not read as the default.
	unknown := SearchMetadataParamsMatch("preifx")
	r, err := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "ord", Match: &unknown}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(SearchMetadata422JSONResponse); !ok {
		t.Errorf("match=preifx = %T, want 422", r)
	}
}

func TestSearch_ExcludesTombstoned(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	r, err := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: "orders_old"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range r.(SearchMetadata200JSONResponse) {
		if h.TableId == f.removed {
			t.Fatal("a removed table is a search hit")
		}
	}
}

// TestMetadataTable_DisplayForBinding: types and the qualified name are
// rendered for the binding's engine (here Trino); a tombstoned table shows
// removed_at; a binding of another catalog is a 404.
func TestMetadataTable_DisplayForBinding(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	r, err := f.h.GetMetadataTable(f.as(f.owner), GetMetadataTableRequestObject{Id: f.ws, TableId: f.orders})
	if err != nil {
		t.Fatal(err)
	}
	tb := r.(GetMetadataTable200JSONResponse)
	if tb.QualifiedName == nil || *tb.QualifiedName != `"lake_t"."sales"."orders"` || tb.BindingId == nil || *tb.BindingId != f.binding {
		t.Fatalf("qualified name %v binding %v", tb.QualifiedName, tb.BindingId)
	}
	if tb.PreviewSql == nil || *tb.PreviewSql != `SELECT * FROM "lake_t"."sales"."orders" LIMIT 100` {
		t.Fatalf("preview sql %v", tb.PreviewSql)
	}
	if len(tb.Columns) != 3 || *tb.Columns[0].DisplayType != "bigint" || *tb.Columns[1].DisplayType != "array(varchar)" || tb.Columns[1].NativeType != "list<string>" {
		t.Fatalf("columns = %+v", tb.Columns)
	}
	r, _ = f.h.GetMetadataTable(f.as(f.owner), GetMetadataTableRequestObject{Id: f.ws, TableId: f.removed})
	if tb := r.(GetMetadataTable200JSONResponse); tb.RemovedAt == nil {
		t.Fatal("a tombstoned table shows removed_at")
	}
	foreign := openapi_types.UUID{0xFF}
	if r, _ := f.h.GetMetadataTable(f.as(f.owner), GetMetadataTableRequestObject{Id: f.ws, TableId: f.orders, Params: GetMetadataTableParams{BindingId: &foreign}}); !is404(r) {
		t.Fatalf("unknown binding: %T", r)
	}
}

// TestSearch_Filters: tags, the certified tag and namespace compose with the
// free text and with each other (#534).
func TestSearch_Filters(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	for name, doc := range map[string]struct {
		tags      string
		certified bool
	}{
		"orders":                 {tags: `{finance}`, certified: true},
		"customer_order_history": {tags: `{sales}`},
	} {
		wsID := model.UUID(uuid.UUID(f.ws).String())
		row := model.SemanticDoc{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: wsID,
			CatalogID:      model.UUID(uuid.UUID(f.lake).String()),
			Namespace:      model.StringArray{"sales"},
			TableNameValue: model.Text(name),
			Description:    "",
			Tags:           model.StringArray(strings.Split(strings.Trim(doc.tags, "{}"), ",")),
			Certified:      doc.certified,
			UpdatedBy:      model.UUID(f.owner),
		}
		if err := f.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	search := func(params SearchMetadataParams) SearchMetadata200JSONResponse {
		t.Helper()
		params.Q = "order"
		resp, err := f.h.SearchMetadata(f.as(f.owner), SearchMetadataRequestObject{Id: f.ws, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		page, ok := resp.(SearchMetadata200JSONResponse)
		if !ok {
			t.Fatalf("response = %T", resp)
		}
		return page
	}

	finance := []string{"finance"}
	for _, hit := range search(SearchMetadataParams{Tags: &finance}) {
		if hit.Table != "orders" {
			t.Fatalf("tag:finance returned %s", hit.Table)
		}
	}
	if got := len(search(SearchMetadataParams{Tags: &finance})); got == 0 {
		t.Fatal("tag:finance returned nothing")
	}

	for _, hit := range search(SearchMetadataParams{Certified: ptr(true)}) {
		if hit.Table != "orders" {
			t.Fatalf("certified:yes returned %s", hit.Table)
		}
	}

	sales := []string{"sales"}
	for _, hit := range search(SearchMetadataParams{Namespace: &sales}) {
		if hit.Table != "orders" && hit.Table != "customer_order_history" {
			t.Fatalf("namespace:sales returned %s", hit.Table)
		}
	}
	ops := []string{"ops"}
	if got := search(SearchMetadataParams{Namespace: &ops}); len(got) != 0 {
		t.Fatalf("namespace:ops returned %d hits", len(got))
	}
}
