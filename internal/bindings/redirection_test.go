package bindings

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeRedirectionStore struct {
	bindings []db.CatalogBinding
	catalogs map[[16]byte]db.Catalog
}

func (s fakeRedirectionStore) ListBindingsByEngine(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return s.bindings, nil
}

func (s fakeRedirectionStore) GetCatalogByID(_ context.Context, id pgtype.UUID) (db.Catalog, error) {
	c, ok := s.catalogs[id.Bytes]
	if !ok {
		return db.Catalog{}, pgx.ErrNoRows
	}
	return c, nil
}

func uuid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

func strptr(s string) *string { return &s }

// TestRedirectionProperties: a Hive-connector catalog of a metastore whose
// connection also exposes Iceberg and Delta catalogs on the same engine
// delegates those tables to them (#86).
func TestRedirectionProperties(t *testing.T) {
	conn, engine := uuid(1), uuid(2)
	hiveCat, iceCat, deltaCat, otherConnCat := uuid(10), uuid(11), uuid(12), uuid(13)
	store := fakeRedirectionStore{
		bindings: []db.CatalogBinding{
			{CatalogID: iceCat, EngineCatalogName: "lake", Connector: strptr("iceberg"), Enabled: true, LifecycleState: StateActive},
			{CatalogID: deltaCat, EngineCatalogName: "delta", Connector: strptr("delta_lake"), Enabled: true, LifecycleState: StateActive},
			{CatalogID: otherConnCat, EngineCatalogName: "theirs", Connector: strptr("iceberg"), Enabled: true, LifecycleState: StateActive},
			{CatalogID: uuid(14), EngineCatalogName: "pending", Connector: strptr("iceberg"), Enabled: true, LifecycleState: StateProvisioning},
			{CatalogID: uuid(15), EngineCatalogName: "off", Connector: strptr("iceberg"), Enabled: false, LifecycleState: StateActive},
			{CatalogID: hiveCat, EngineCatalogName: "self", Connector: strptr("hive"), Enabled: true, LifecycleState: StateActive},
		},
		catalogs: map[[16]byte]db.Catalog{
			iceCat.Bytes:       {ID: iceCat, ConnectionID: conn, Enabled: true},
			deltaCat.Bytes:     {ID: deltaCat, ConnectionID: conn, Enabled: true},
			otherConnCat.Bytes: {ID: otherConnCat, ConnectionID: uuid(9), Enabled: true},
			uuid(14).Bytes:     {ID: uuid(14), ConnectionID: conn, Enabled: true},
			uuid(15).Bytes:     {ID: uuid(15), ConnectionID: conn, Enabled: true},
		},
	}

	extra := map[string]string{}
	if err := HiveRedirection(context.Background(), store, connections.KindHiveMetastore, conn, engine, hiveCat, "hive", extra); err != nil {
		t.Fatal(err)
	}
	if extra["hive.iceberg-catalog-name"] != "lake" || extra["hive.delta-lake-catalog-name"] != "delta" {
		t.Errorf("redirection = %v", extra)
	}
	if len(extra) != 2 {
		t.Errorf("only active siblings of the same connection on the same engine: %v", extra)
	}

	// A no-op for anything but a metastore through the hive connector.
	for _, c := range []struct {
		kind      connections.Kind
		connector string
	}{
		{connections.KindPostgreSQL, "hive"},
		{connections.KindHiveMetastore, "iceberg"},
		{connections.KindHiveMetastore, ""},
	} {
		got := map[string]string{}
		if err := HiveRedirection(context.Background(), store, c.kind, conn, engine, hiveCat, c.connector, got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("%s/%q: %v", c.kind, c.connector, got)
		}
	}
}
