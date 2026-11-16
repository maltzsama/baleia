package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
)

// lineageFixture extends metaFixture with content over the `orders` table: one
// model, one metric and one saved query, all readable by the owner.
type lineageFixture struct {
	*metaFixture
	space     db.UUID
	model     db.UUID
	metric    db.UUID
	saved     db.UUID
	ownerUUID db.UUID
}

func newLineageFixture(t *testing.T) *lineageFixture {
	t.Helper()
	f := &lineageFixture{metaFixture: newMetaFixture(t)}
	ctx := context.Background()
	ws := db.UUID{Bytes: f.ws, Valid: true}
	f.ownerUUID = db.UUID{Bytes: uuid.MustParse(f.owner), Valid: true}

	space, err := f.h.queries.GetDefaultSpace(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	f.space = space.ID

	def := []byte(`{"source":{"kind":"table","namespace":["sales"],"table":"orders"},
		"primary_key":["id"],
		"dimensions":[{"name":"id","type":"int64"},{"name":"note","sql":"order_note","type":"string"}],
		"metrics":[{"name":"orders_count","aggregation":"count"}]}`)
	m, err := f.h.queries.CreateSemanticModel(ctx, db.CreateSemanticModelParams{
		WorkspaceID: ws, SpaceID: space.ID, Slug: "lineage_orders", Label: "Orders",
		CatalogID:  db.UUID{Bytes: f.lake, Valid: true},
		BindingID:  db.UUID{Bytes: f.binding, Valid: true},
		Definition: def, FormatVersion: 1,
		OwnerUserID: f.ownerUUID, UpdatedBy: f.ownerUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.model = m.ID

	mt, err := f.h.queries.CreateSemanticMetric(ctx, db.CreateSemanticMetricParams{
		WorkspaceID: ws, SpaceID: space.ID, Slug: "lineage_orders_metric", Label: "Orders metric",
		CatalogID: db.UUID{Bytes: f.lake, Valid: true}, Namespace: []string{"sales"}, TableName: "orders",
		BindingID:  db.UUID{Bytes: f.binding, Valid: true},
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1,
		OwnerUserID: f.ownerUUID, UpdatedBy: f.ownerUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.metric = mt.ID

	bindingID := model.UUID(repo.UUIDString(db.UUID{Bytes: f.binding, Valid: true}))
	savedQuery := model.SavedQuery{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(repo.UUIDString(ws)),
		SpaceID: model.UUID(repo.UUIDString(space.ID)), CreatedBy: model.UUID(f.owner),
		Name: "Orders query", SQL: "select * from sales.orders", Slug: "lineage_orders_query",
		TargetBindingID: &bindingID, TargetSchema: ptrTo("sales"),
	}
	if err := f.gdb.Create(&savedQuery).Error; err != nil {
		t.Fatal(err)
	}
	f.saved = repo.PGUUID(string(savedQuery.ID))
	return f
}

func nodeByKind(nodes []LineageNode, kind LineageNodeKind) []LineageNode {
	var out []LineageNode
	for _, n := range nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// TestLineage_TableUp lists every content item that reads the orders table.
func TestLineage_TableUp(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.owner)

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id: f.ws,
		Params: ListLineageParams{
			Node:      "table:" + idString(db.UUID{Bytes: f.orders, Valid: true}),
			Direction: ptr(Up),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListLineage200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}

	kindCount := func(kind LineageNodeKind) int { return len(nodeByKind(got.Nodes, kind)) }
	if kindCount(LineageNodeKindTable) != 1 || kindCount(LineageNodeKindModel) != 1 ||
		kindCount(LineageNodeKindMetric) != 1 || kindCount(LineageNodeKindSavedQuery) != 1 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	if len(got.Edges) != 3 {
		t.Fatalf("edges = %+v, want 3", got.Edges)
	}
	// The table node links to the data explorer, on this workspace's table.
	tables := nodeByKind(got.Nodes, LineageNodeKindTable)
	wantHref := "/data?catalog=" + idString(db.UUID{Bytes: f.lake, Valid: true}) +
		"&ns=sales&table=orders&ws=" + idString(db.UUID{Bytes: f.ws, Valid: true})
	if tables[0].Href == nil || *tables[0].Href != wantHref {
		t.Errorf("table href = %v, want %q", tables[0].Href, wantHref)
	}
	// Every edge is "content depends on table": to == the table node.
	tableID := "table:" + idString(db.UUID{Bytes: f.orders, Valid: true})
	for _, e := range got.Edges {
		if e.To != tableID {
			t.Errorf("edge %+v does not point at the table", e)
		}
	}
}

// TestLineage_ModelDown lists the model and its base table only.
func TestLineage_ModelDown(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.owner)

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id: f.ws,
		Params: ListLineageParams{
			Node: "model:" + idString(f.model),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListLineage200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("nodes=%+v edges=%+v, want 2 nodes and 1 edge", got.Nodes, got.Edges)
	}
	if got.Edges[0].From != "model:"+idString(f.model) {
		t.Errorf("edge = %+v", got.Edges[0])
	}
}

// TestLineage_TableDown returns only the table: a table depends on nothing.
func TestLineage_TableDown(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.owner)

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id:     f.ws,
		Params: ListLineageParams{Node: "table:" + idString(db.UUID{Bytes: f.orders, Valid: true})},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(ListLineage200JSONResponse)
	if len(got.Nodes) != 1 || len(got.Edges) != 0 {
		t.Fatalf("nodes=%+v edges=%+v, want just the table", got.Nodes, got.Edges)
	}
}

// TestLineage_ForeignWorkspace404 and ungranted tables are invisible.
func TestLineage_ForeignWorkspace404(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.stranger)

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id:     f.ws,
		Params: ListLineageParams{Node: "table:" + idString(db.UUID{Bytes: f.orders, Valid: true})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListLineage404JSONResponse); !ok {
		t.Fatalf("response = %T, want 404", resp)
	}
}

// TestLineage_SavedQueryUsesTargetDialect: a saved query whose target runs on
// a T-SQL engine is parsed with T-SQL, so a bracket-quoted table still
// resolves to an edge (#1091). The catalog also carries an older Trino binding
// (the generic and Trino dialects read no table from `[sales].[orders]`), so a
// lookup by catalog alone would pick the wrong dialect.
func TestLineage_SavedQueryUsesTargetDialect(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.owner)

	connection := func(name, kind, url string) model.UUID {
		t.Helper()
		row := model.CatalogConnection{ID: model.UUID(repo.NewUUID()), Name: model.Text(name), Type: kind, URL: url}
		if err := f.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	engine := func(name, kind string, conn model.UUID) model.UUID {
		t.Helper()
		eng, err := f.h.queries.CreateEngine(ctx, db.CreateEngineParams{
			Name: name, Kind: kind, Class: "direct", ConnectionID: repo.PGUUID(string(conn)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return model.UUID(eng.ID.String())
	}

	mssqlConn := connection("mssql", "sqlserver", "sqlserver://x")
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: mssqlConn,
		Name: "mssql_catalog", Origin: "native", Enabled: true,
	}
	if err := f.gdb.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	// Two enabled bindings on one catalog: an older Trino one and the saved
	// query's T-SQL target.
	trino := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		EngineID:          engine("lineage_trino", "trino", connection("trino", "trino", "http://t")),
		EngineCatalogName: "mssql_catalog_t", Mode: "managed", Enabled: true,
		CreatedAt: time.Now().Add(-time.Hour),
	}
	if err := f.gdb.Create(&trino).Error; err != nil {
		t.Fatal(err)
	}
	mssql := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		EngineID:          engine("lineage_mssql", "sqlserver", mssqlConn),
		EngineCatalogName: "mssql_catalog_m", Mode: "managed", Enabled: true,
	}
	if err := f.gdb.Create(&mssql).Error; err != nil {
		t.Fatal(err)
	}
	table := model.MetadataTable{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		Namespace: model.StringArray{"sales"}, Name: "orders", Kind: "table", Fingerprint: "f",
		LastSeenAt: time.Now(), LastChangedAt: time.Now(),
	}
	if err := f.gdb.Create(&table).Error; err != nil {
		t.Fatal(err)
	}

	bindingID := mssql.ID
	savedQuery := model.SavedQuery{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(repo.UUIDString(db.UUID{Bytes: f.ws, Valid: true})),
		SpaceID: model.UUID(repo.UUIDString(f.space)), CreatedBy: model.UUID(f.owner),
		Name: "MSSQL orders", SQL: "select * from [sales].[orders]", Slug: "lineage_mssql_orders",
		TargetBindingID: &bindingID, TargetSchema: ptrTo("sales"),
	}
	if err := f.gdb.Create(&savedQuery).Error; err != nil {
		t.Fatal(err)
	}

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id: f.ws,
		Params: ListLineageParams{
			Node: "saved_query:" + idString(repo.PGUUID(string(savedQuery.ID))),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListLineage200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("nodes=%+v edges=%+v, want the query and its table", got.Nodes, got.Edges)
	}
	want := "table:" + idString(repo.PGUUID(string(table.ID)))
	if got.Edges[0].To != want {
		t.Errorf("edge = %+v, want to %s", got.Edges[0], want)
	}
}

// TestSQLRefsDialect: the extractor uses the dialect it is given, and a nil
// dialect keeps the generic one. T-SQL reads a bracket-quoted table the
// generic dialect cannot.
func TestSQLRefsDialect(t *testing.T) {
	t.Parallel()
	catalog := db.UUID{Bytes: [16]byte{1}, Valid: true}
	tsql := dialect.NewRegistry().MustGet(dialect.TSQL)

	got := sqlRefs("select * from [sales].[orders]", catalog, []string{"sales"}, tsql)
	if len(got) != 1 || got[0].name != "orders" || len(got[0].namespace) != 1 || got[0].namespace[0] != "sales" {
		t.Errorf("T-SQL refs = %+v, want sales.orders", got)
	}
	if got := sqlRefs("select * from [sales].[orders]", catalog, []string{"sales"}, nil); len(got) != 0 {
		t.Errorf("generic refs = %+v, want none", got)
	}
}

// TestLineage_InvalidNode is a 404, never an error.
func TestLineage_InvalidNode(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ctx := f.as(f.owner)

	resp, err := f.h.ListLineage(ctx, ListLineageRequestObject{
		Id:     f.ws,
		Params: ListLineageParams{Node: "nonsense"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListLineage404JSONResponse); !ok {
		t.Fatalf("response = %T, want 404", resp)
	}
}
