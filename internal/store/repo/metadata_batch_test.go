package repo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
)

// TestListMetadataCatalogsBatchesBindings covers the batched binding/sync
// resolution across several catalogs: each catalog's bindings must attribute
// to its own engine, and per-catalog sync times must not bleed across rows.
func TestListMetadataCatalogsBatchesBindings(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	gdb := f.gdb
	now := time.Now().UTC()

	conn2 := model.CatalogConnection{ID: model.UUID(NewUUID()), Name: model.Text("batch-conn-" + NewUUID()[:8]), Type: "polaris", URL: "http://p2.test", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&conn2).Error; err != nil {
		t.Fatalf("conn2: %v", err)
	}
	catalog2 := model.Catalog{ID: model.UUID(NewUUID()), ConnectionID: conn2.ID, Name: model.Text("lake2_" + NewUUID()[:8]), Origin: "native", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&catalog2).Error; err != nil {
		t.Fatalf("catalog2: %v", err)
	}
	if err := gdb.Create(&model.WorkspaceCatalogGrant{WorkspaceID: f.ws, CatalogID: catalog2.ID, GrantedBy: f.owner, GrantedAt: now}).Error; err != nil {
		t.Fatalf("grant2: %v", err)
	}
	engineConn2 := model.CatalogConnection{ID: model.UUID(NewUUID()), Name: model.Text("batch-eng-" + NewUUID()[:8]), Type: "trino", URL: "http://t2.test", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&engineConn2).Error; err != nil {
		t.Fatalf("engineConn2: %v", err)
	}
	engine2 := model.Engine{ID: model.UUID(NewUUID()), Name: model.Text("trino2"), Kind: "trino", Class: "federated", ConnectionID: engineConn2.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&engine2).Error; err != nil {
		t.Fatalf("engine2: %v", err)
	}
	binding2 := model.CatalogBinding{ID: model.UUID(NewUUID()), CatalogID: catalog2.ID, EngineID: engine2.ID, EngineCatalogName: "lake2_t", Mode: "adopted", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&binding2).Error; err != nil {
		t.Fatalf("binding2: %v", err)
	}

	older := now.Add(-10 * time.Minute)
	if err := gdb.Create(&model.MetadataSyncRun{ID: model.UUID(NewUUID()), CatalogID: f.catalog, StartedAt: older, FinishedAt: &older, Status: "finished", SourcePath: "provider"}).Error; err != nil {
		t.Fatalf("run1: %v", err)
	}
	fresh := now.Add(-time.Minute)
	if err := gdb.Create(&model.MetadataSyncRun{ID: model.UUID(NewUUID()), CatalogID: catalog2.ID, StartedAt: fresh, FinishedAt: &fresh, Status: "finished", SourcePath: "provider"}).Error; err != nil {
		t.Fatalf("run2: %v", err)
	}

	rows, err := f.r.ListMetadataCatalogs(ctx, f.wsID())
	if err != nil {
		t.Fatalf("ListMetadataCatalogs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("catalogs = %d, want 2", len(rows))
	}
	byCatalog := make(map[string]db.ListMetadataCatalogsRow, len(rows))
	for _, row := range rows {
		byCatalog[UUIDString(row.ID)] = row
	}

	checkBindings := func(catalogID model.UUID, wantEngine string) {
		t.Helper()
		row, ok := byCatalog[string(catalogID)]
		if !ok {
			t.Fatalf("missing catalog %s in %v", catalogID, rows)
		}
		var bindings []metadataBinding
		if err := json.Unmarshal(row.Bindings, &bindings); err != nil {
			t.Fatalf("unmarshal bindings for %s: %v", catalogID, err)
		}
		if len(bindings) != 1 || bindings[0].EngineName != wantEngine || bindings[0].Status != "synced" {
			t.Fatalf("bindings for %s = %+v, want one binding engine %q status synced", catalogID, bindings, wantEngine)
		}
	}
	checkBindings(f.catalog, "trino")
	checkBindings(catalog2.ID, "trino2")

	// Sync times must be attributed per catalog: the first is 10m stale, the
	// second 1m.
	c1 := byCatalog[string(f.catalog)].MetadataSyncedAt
	c2 := byCatalog[string(catalog2.ID)].MetadataSyncedAt
	if !c1.Valid || !c2.Valid || !c1.Time.Before(c2.Time) {
		t.Fatalf("syncedAt c1=%v c2=%v, want c1 older than c2", c1.Time, c2.Time)
	}

	maxAge, err := f.r.MaxMetadataStalenessSeconds(ctx)
	if err != nil {
		t.Fatalf("MaxMetadataStalenessSeconds: %v", err)
	}
	if maxAge < 9*60 {
		t.Fatalf("staleness = %v, want >= 9m (the stalest catalog is 10m old)", maxAge)
	}
}
