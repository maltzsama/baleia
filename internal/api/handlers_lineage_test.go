package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
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
