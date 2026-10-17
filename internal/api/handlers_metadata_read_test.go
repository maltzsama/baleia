package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// metaFixture is a database with two users, each with a personal workspace;
// the first workspace is granted catalog "lake" (Iceberg, bound into Trino),
// not catalog "other", and its owner owns catalog "mine".
type metaFixture struct {
	pool            *pgxpool.Pool
	h               *Handlers
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
	ctx := context.Background()
	f := &metaFixture{pool: pool}
	scan := func(dst *openapi_types.UUID, q string, args ...any) {
		t.Helper()
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		*dst = openapi_types.UUID(id.Bytes)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var ownerID, strangerID openapi_types.UUID
	scan(&ownerID, `INSERT INTO users (name, email, password_hash) VALUES ('o', 'o@x', 'x') RETURNING id`)
	scan(&strangerID, `INSERT INTO users (name, email, password_hash) VALUES ('s', 's@x', 'x') RETURNING id`)
	f.owner, f.stranger = ownerID.String(), strangerID.String()
	scan(&f.ws, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id`, ownerID)
	scan(&f.strangerWS, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w2', 'personal', 'p2', $1) RETURNING id`, strangerID)

	var conn, trinoConn, engine openapi_types.UUID
	scan(&conn, `INSERT INTO catalog_connections (name, type, url) VALUES ('pol', 'polaris', 'http://p') RETURNING id`)
	scan(&trinoConn, `INSERT INTO catalog_connections (name, type, url) VALUES ('tri', 'trino', 'http://t') RETURNING id`)
	scan(&engine, `INSERT INTO engines (name, kind, class, connection_id) VALUES ('trino', 'trino', 'federated', $1) RETURNING id`, trinoConn)
	scan(&f.lake, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'lake', 'native') RETURNING id`, conn)
	scan(&f.other, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'other', 'native') RETURNING id`, conn)
	var ownConn openapi_types.UUID
	scan(&ownConn, `INSERT INTO catalog_connections (name, type, url, created_by) VALUES ('own', 'postgresql', 'postgres://x', $1) RETURNING id`, ownerID)
	scan(&f.mine, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'mine', 'native') RETURNING id`, ownConn)
	scan(&f.binding, `INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode) VALUES ($1, $2, 'lake_t', 'adopted') RETURNING id`, f.lake, engine)
	exec(`INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, permission, granted_by) VALUES ($1, $2, 'read', $3)`, f.ws, f.lake, ownerID)
	exec(`INSERT INTO metadata_sync_runs (catalog_id, status, source_path, finished_at) VALUES ($1, 'finished', 'provider', now())`, f.lake)

	for _, ns := range []string{`{sales}`, `{ops}`, `{ops,"b.c"}`} {
		exec(`INSERT INTO metadata_namespaces (catalog_id, namespace, last_seen_at) VALUES ($1, $2, now())`, f.lake, ns)
	}
	table := func(dst *openapi_types.UUID, cat openapi_types.UUID, ns, name string, removed bool) {
		t.Helper()
		q := `INSERT INTO metadata_tables (catalog_id, namespace, name, kind, fingerprint, last_seen_at, last_changed_at, removed_at)
		      VALUES ($1, $2, $3, 'table', 'f', now(), now(), CASE WHEN $4 THEN now() END) RETURNING id`
		scan(dst, q, cat, ns, name, removed)
	}
	table(&f.orders, f.lake, `{sales}`, "orders", false)
	exec(`INSERT INTO metadata_columns (table_id, ordinal, name, native_type, type_system, nullable)
	      VALUES ($1, 1, 'id', 'long', 'iceberg', false), ($1, 2, 'tags', 'list<string>', 'iceberg', true), ($1, 3, 'order_note', 'string', 'iceberg', true)`, f.orders)
	var hist openapi_types.UUID
	table(&hist, f.lake, `{sales}`, "customer_order_history", false)
	table(&f.removed, f.lake, `{sales}`, "orders_old", true)
	table(&f.otherTable, f.other, `{sales}`, "orders_secret", false)
	var deep openapi_types.UUID
	table(&deep, f.lake, `{ops,"b.c"}`, "deep", false)

	f.h = &Handlers{queries: db.New(pool)} // no engines: reads never call one
	return f
}

func (f *metaFixture) as(user string) context.Context { return ctxAs(user, "member") }

func TestMetadata_ForeignWorkspace404(t *testing.T) {
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
	// The owner's own catalog is visible in their personal workspace (#16).
	if len(list) != 2 || list[1].Id != f.mine || list[1].MetadataSyncStatus != "never" || len(list[1].Bindings) != 0 {
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
	f := newMetaFixture(t)
	ctx := context.Background()
	for i := range 250 {
		if _, err := f.pool.Exec(ctx, `INSERT INTO metadata_tables (catalog_id, namespace, name, kind, fingerprint, last_seen_at, last_changed_at)
			VALUES ($1, '{bulk}', $2, 'table', 'f', now(), now())`, f.lake, fmt.Sprintf("t%03d", 249-i)); err != nil {
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

func TestSearch_ExcludesTombstoned(t *testing.T) {
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
