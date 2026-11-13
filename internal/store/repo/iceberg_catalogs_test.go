package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
)

// TestListEnabledIcebergCatalogs: the table-health fan-out lists the enabled
// source catalogs of exactly the kinds it is given (#996).
func TestListEnabledIcebergCatalogs(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	r := New(gdb)

	cluster := model.TrinoCluster{
		ID: model.UUID(NewUUID()), Name: model.Text("iceberg-kinds-cluster"),
		CoordinatorURL: "http://trino.test", CreatedAt: time.Now().UTC(),
	}
	if err := gdb.Create(&cluster).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	catalog := func(kind, name string) db.Catalog {
		t.Helper()
		connID := db.UUID{Bytes: uuid.New(), Valid: true}
		if _, err := r.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
			ID: connID, Name: kind + "-" + name, Type: kind, Url: "http://" + kind + ".test", Config: []byte(`{}`),
		}); err != nil {
			t.Fatalf("CreateCatalogConnection %s: %v", kind, err)
		}
		c, err := r.CreateCatalog(ctx, db.CreateCatalogParams{
			ConnectionID: connID, Name: name, TrinoCatalogName: strPtr(name), ClusterID: PGUUID(string(cluster.ID)),
			Warehouse: strPtr("wh_" + name), Connector: strPtr("iceberg"), ExtraProps: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("CreateCatalog %s: %v", name, err)
		}
		return c
	}
	catalog("polaris", "lake")
	catalog("unity", "uc")
	catalog("glue", "glue_iceberg")
	off := catalog("lakekeeper", "lk_off")
	if err := r.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: off.ID, Enabled: false}); err != nil {
		t.Fatalf("SetCatalogEnabled: %v", err)
	}

	names := func(kinds []string) []string {
		t.Helper()
		rows, err := r.ListEnabledIcebergCatalogs(ctx, kinds)
		if err != nil {
			t.Fatalf("ListEnabledIcebergCatalogs(%v): %v", kinds, err)
		}
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.Name
		}
		return out
	}
	if got := names([]string{"iceberg", "lakekeeper", "nessie", "polaris", "unity"}); len(got) != 2 || got[0] != "lake" || got[1] != "uc" {
		t.Errorf("Iceberg kinds = %v, want [lake uc]", got)
	}
	if got := names([]string{"polaris"}); len(got) != 1 || got[0] != "lake" {
		t.Errorf("polaris only = %v, want [lake]", got)
	}
	if got := names(nil); len(got) != 0 {
		t.Errorf("no kinds = %v, want none", got)
	}
}
