package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
)

// TestListHiveBindingsToResync: the GORM query returns only the active hive
// binding of this connection on the engine, so a sibling change re-applies the
// right catalog.
func TestListHiveBindingsToResync(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	r := New(gdb)
	ctx := context.Background()
	now := time.Now().UTC()

	var cluster model.TrinoCluster
	if err := gdb.Where("name = ?", "default").First(&cluster).Error; err != nil {
		t.Fatalf("default cluster: %v", err)
	}
	var engine model.Engine
	if err := gdb.Where("name = ?", "default").First(&engine).Error; err != nil {
		t.Fatalf("default engine: %v", err)
	}

	connection := func(name string) model.CatalogConnection {
		c := model.CatalogConnection{
			ID: model.UUID(uuid.NewString()), Name: model.Text(name), Type: "hive",
			URL: "", Config: model.JSON(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := gdb.Create(&c).Error; err != nil {
			t.Fatalf("connection %q: %v", name, err)
		}
		return c
	}
	catalog := func(conn model.CatalogConnection, name, connector string) model.Catalog {
		trinoName := model.Text(name)
		connText := model.Text(connector)
		c := model.Catalog{
			ID: model.UUID(uuid.NewString()), ConnectionID: conn.ID, Name: model.Text(name),
			TrinoCatalogName: &trinoName, ClusterID: &cluster.ID, Warehouse: strPtr(""),
			Connector: &connText, Origin: "source", Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := gdb.Create(&c).Error; err != nil {
			t.Fatalf("catalog %q: %v", name, err)
		}
		return c
	}
	binding := func(cat model.Catalog, connector string, enabled bool) model.CatalogBinding {
		b := model.CatalogBinding{
			ID: model.UUID(uuid.NewString()), CatalogID: cat.ID, EngineID: engine.ID,
			EngineCatalogName: model.Text("c_" + uuid.NewString()[:8]), Connector: strPtr(connector),
			Mode: "managed", Enabled: enabled, LifecycleState: "active", CreatedAt: now, UpdatedAt: now,
		}
		if err := gdb.Create(&b).Error; err != nil {
			t.Fatalf("binding: %v", err)
		}
		if !enabled {
			// GORM applies the column default to a zero value on Create.
			if err := gdb.Model(&model.CatalogBinding{}).Where("id = ?", string(b.ID)).Update("enabled", false).Error; err != nil {
				t.Fatalf("disable binding: %v", err)
			}
			b.Enabled = false
		}
		return b
	}

	ours := connection("hive-src")
	theirs := connection("hive-other")
	want := binding(catalog(ours, "hive_ours", "hive"), "hive", true)
	binding(catalog(theirs, "hive_theirs", "hive"), "hive", true)
	binding(catalog(ours, "hive_off", "hive"), "hive", false)
	binding(catalog(ours, "ice", "iceberg"), "iceberg", true)

	got, err := r.ListHiveBindingsToResync(ctx, PGUUID(string(ours.ID)), PGUUID(string(engine.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != PGUUID(string(want.ID)) {
		t.Fatalf("bindings = %v, want only %s", got, want.ID)
	}
}
