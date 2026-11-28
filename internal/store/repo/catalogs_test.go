package repo

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maltzsama/baleia/internal/db"
	"github.com/maltzsama/baleia/internal/store/model"
	"github.com/maltzsama/baleia/internal/testdb"
)

// TestCatalogConfig: a catalog's own configuration (#1064) is stored on create,
// kept by an update that does not carry one, and replaced by one that does.
func TestCatalogConfig(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	r := New(gdb)

	cluster := model.TrinoCluster{
		ID: model.UUID(NewUUID()), Name: model.Text("catalog-config-cluster"),
		CoordinatorURL: "http://trino.test", CreatedAt: time.Now().UTC(),
	}
	if err := gdb.Create(&cluster).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	connID := db.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := r.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: connID, Name: "events", Type: "polaris", Url: "http://polaris.test", Config: []byte(`{}`),
	}); err != nil {
		t.Fatalf("CreateCatalogConnection: %v", err)
	}

	config := func(c db.Catalog) map[string]string {
		t.Helper()
		values, err := c.ConfigValues()
		if err != nil {
			t.Fatalf("ConfigValues: %v", err)
		}
		return values
	}
	reread := func(id db.UUID) db.Catalog {
		t.Helper()
		c, err := r.GetCatalogByID(ctx, id)
		if err != nil {
			t.Fatalf("GetCatalogByID: %v", err)
		}
		return c
	}

	plain, err := r.CreateCatalog(ctx, db.CreateCatalogParams{
		ConnectionID: connID, Name: "plain", TrinoCatalogName: strPtr("plain"), ClusterID: PGUUID(string(cluster.ID)),
		Warehouse: strPtr("wh"), Connector: strPtr("iceberg"), ExtraProps: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateCatalog without config: %v", err)
	}
	if got := config(reread(plain.ID)); len(got) != 0 {
		t.Errorf("a catalog created without config = %v, want empty", got)
	}

	topics := map[string]string{"topics": "orders,payments", "schema": "default"}
	created, err := r.CreateCatalog(ctx, db.CreateCatalogParams{
		ConnectionID: connID, Name: "stream", TrinoCatalogName: strPtr("stream"), ClusterID: PGUUID(string(cluster.ID)),
		Warehouse: strPtr(""), Connector: strPtr("kafka"), ExtraProps: []byte(`{}`),
		Config: []byte(`{"topics":"orders,payments","schema":"default"}`),
	})
	if err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if got := config(reread(created.ID)); !maps.Equal(got, topics) {
		t.Errorf("stored config = %v, want %v", got, topics)
	}

	update := func(cfg []byte) db.Catalog {
		t.Helper()
		c := reread(created.ID)
		updated, err := r.UpdateCatalog(ctx, db.UpdateCatalogParams{
			ID: c.ID, Name: c.Name, Description: c.Description, Warehouse: c.Warehouse,
			Connector: c.Connector, ExtraProps: c.ExtraProps, TrinoCatalogName: c.TrinoCatalogName, Config: cfg,
		})
		if err != nil {
			t.Fatalf("UpdateCatalog: %v", err)
		}
		return updated
	}
	if got := config(update(nil)); !maps.Equal(got, topics) {
		t.Errorf("after an update without config = %v, want it kept: %v", got, topics)
	}
	replaced := map[string]string{"topics": "payments", "schema": "events"}
	if got := config(update([]byte(`{"topics":"payments","schema":"events"}`))); !maps.Equal(got, replaced) {
		t.Errorf("after an update with config = %v, want %v", got, replaced)
	}
}
