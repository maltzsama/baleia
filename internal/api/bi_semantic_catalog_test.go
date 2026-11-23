package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semanticcatalog"
	"github.com/google/uuid"
)

// semanticCatalogEngine returns the engine the fixture binds the lake catalog
// to: the engine a BI key of the workspace would carry.
func semanticCatalogEngine(t *testing.T, f *lineageFixture) db.UUID {
	t.Helper()
	targets, err := f.h.access.Targets(context.Background(), db.UUID{Bytes: f.ws, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.BindingID.Bytes == f.binding {
			return target.EngineID
		}
	}
	t.Fatal("the fixture binding is not a workspace target")
	return db.UUID{}
}

func TestBISemanticCatalog_ModelsAndMetrics(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	ws := db.UUID{Bytes: f.ws, Valid: true}
	// A model on a catalog the workspace was never granted: listed, but its
	// table is not named.
	_, err := f.h.queries.CreateSemanticModel(context.Background(), db.CreateSemanticModelParams{
		WorkspaceID: ws, SpaceID: f.space, Slug: "secret_orders", Label: "Secret",
		CatalogID:     db.UUID{Bytes: f.other, Valid: true},
		Definition:    []byte(`{"source":{"kind":"table","namespace":["sales"],"table":"orders_secret"},"dimensions":[{"name":"id","type":"int64"}]}`),
		FormatVersion: 1, OwnerUserID: f.ownerUUID, UpdatedBy: f.ownerUUID,
	})
	if err != nil {
		t.Fatal(err)
	}

	reader := f.h.BISemanticCatalog("https://baleia.example/")
	snap, err := reader.Read(context.Background(), semanticcatalog.Request{
		WorkspaceID: ws, UserID: f.ownerUUID, EngineID: semanticCatalogEngine(t, f),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Workspace.ID != ws.String() || snap.Workspace.Name == "" || snap.Workspace.URL != "https://baleia.example/?ws="+ws.String() {
		t.Fatalf("workspace = %+v", snap.Workspace)
	}
	if len(snap.Models) != 2 || snap.Models[0].Slug != "lineage_orders" || snap.Models[1].Slug != "secret_orders" {
		t.Fatalf("models = %+v", snap.Models)
	}

	orders := snap.Models[0]
	if orders.Label != "Orders" || orders.URL != "https://baleia.example/models/"+orders.ID {
		t.Errorf("model identity = %+v", orders)
	}
	if orders.Source.Kind != "table" || orders.Source.Table != "lake_t.sales.orders" {
		t.Errorf("source = %+v, want the engine's name for the table", orders.Source)
	}
	if !slices.Equal(orders.SourceTables, []string{"lake_t.sales.orders"}) {
		t.Errorf("source tables = %v", orders.SourceTables)
	}
	if len(orders.Columns) != 2 || orders.Columns[0].Name != "id" || orders.Columns[0].Type != "int64" ||
		orders.Columns[1].Name != "note" || orders.Columns[1].Expression != "order_note" {
		t.Errorf("columns = %+v", orders.Columns)
	}
	if len(orders.Metrics) != 1 || orders.Metrics[0].Name != "orders_count" ||
		!strings.Contains(orders.Metrics[0].SQL, `"lake_t"."sales"."orders"`) {
		t.Errorf("model metrics = %+v, want SQL compiled for the key's engine", orders.Metrics)
	}

	secret := snap.Models[1]
	if len(secret.SourceTables) != 0 || secret.Source.Table != "" {
		t.Errorf("an ungranted catalog's table was named: %+v", secret)
	}

	if len(snap.Metrics) != 1 {
		t.Fatalf("metrics = %+v", snap.Metrics)
	}
	metric := snap.Metrics[0]
	if metric.Slug != "lineage_orders_metric" || !slices.Equal(metric.SourceTables, []string{"lake_t.sales.orders"}) ||
		!strings.Contains(metric.SQL, `"lake_t"."sales"."orders"`) || metric.URL != "https://baleia.example/metrics/"+metric.ID {
		t.Errorf("metric = %+v", metric)
	}
}

// TestBISemanticCatalog_OtherEngine: a key on an engine that does not reach
// the workspace's catalogs still lists the content, but names no table and
// renders no SQL.
func TestBISemanticCatalog_OtherEngine(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	snap, err := f.h.BISemanticCatalog("").Read(context.Background(), semanticcatalog.Request{
		WorkspaceID: db.UUID{Bytes: f.ws, Valid: true}, UserID: f.ownerUUID,
		EngineID: db.UUID{Bytes: uuid.New(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Models) != 1 || len(snap.Metrics) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	m, mt := snap.Models[0], snap.Metrics[0]
	if m.Source.Table != "" || len(m.SourceTables) != 0 || m.Metrics[0].SQL != "" || m.URL != "" {
		t.Errorf("model on another engine = %+v", m)
	}
	if mt.SQL != "" || len(mt.SourceTables) != 0 {
		t.Errorf("metric on another engine = %+v", mt)
	}
}

// TestBISemanticCatalog_OnlyVisibleContent: a user who cannot read the
// workspace's spaces sees no content, and saved queries are never listed.
func TestBISemanticCatalog_OnlyVisibleContent(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	stranger := db.UUID{Bytes: uuid.MustParse(f.stranger), Valid: true}
	snap, err := f.h.BISemanticCatalog("").Read(context.Background(), semanticcatalog.Request{
		WorkspaceID: db.UUID{Bytes: f.ws, Valid: true}, UserID: stranger, EngineID: semanticCatalogEngine(t, f),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Models) != 0 || len(snap.Metrics) != 0 {
		t.Fatalf("a non-member saw content: %+v", snap)
	}
}
