package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeEngineStore struct {
	engine     *db.GetDefaultTrinoEngineRow
	cluster    *db.TrinoCluster
	takenNames map[string]bool

	createdConn []db.CreateCatalogConnectionParams
	updated     []db.UpdateCatalogConnectionParams
	engines     []db.CreateEngineParams
	links       []db.LinkTrinoClusterEngineParams
}

func (f *fakeEngineStore) GetDefaultTrinoEngine(context.Context) (db.GetDefaultTrinoEngineRow, error) {
	if f.engine == nil {
		return db.GetDefaultTrinoEngineRow{}, pgx.ErrNoRows
	}
	return *f.engine, nil
}

func (f *fakeEngineStore) GetDefaultTrinoCluster(context.Context) (db.TrinoCluster, error) {
	if f.cluster == nil {
		return db.TrinoCluster{}, pgx.ErrNoRows
	}
	return *f.cluster, nil
}

func (f *fakeEngineStore) CatalogConnectionNameExists(_ context.Context, name string) (bool, error) {
	return f.takenNames[name], nil
}

func (f *fakeEngineStore) CreateCatalogConnection(_ context.Context, p db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	f.createdConn = append(f.createdConn, p)
	return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type}, nil
}

func (f *fakeEngineStore) UpdateCatalogConnection(_ context.Context, p db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	f.updated = append(f.updated, p)
	return db.CatalogConnection{}, nil
}

func (f *fakeEngineStore) CreateEngine(_ context.Context, p db.CreateEngineParams) (db.Engine, error) {
	f.engines = append(f.engines, p)
	return db.Engine{ID: pgtype.UUID{Bytes: [16]byte{9}, Valid: true}, Name: p.Name}, nil
}

func (f *fakeEngineStore) LinkTrinoClusterEngine(_ context.Context, p db.LinkTrinoClusterEngineParams) error {
	f.links = append(f.links, p)
	return nil
}

func defaultEngine(url string, owner pgtype.UUID) *db.GetDefaultTrinoEngineRow {
	cfg, _ := json.Marshal(map[string]string{"url": url})
	return &db.GetDefaultTrinoEngineRow{
		Engine:            db.Engine{Name: "default"},
		CatalogConnection: db.CatalogConnection{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, Url: url, Config: cfg, CreatedBy: owner},
	}
}

func TestEnsureDefaultTrinoEngine(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the engine when the cluster has none", func(t *testing.T) {
		f := &fakeEngineStore{cluster: &db.TrinoCluster{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, Name: "default"}, takenNames: map[string]bool{"trino-default": true}}
		if err := EnsureDefaultTrinoEngine(ctx, f, DefaultTrinoEngine{URL: "http://trino:8080"}); err != nil {
			t.Fatal(err)
		}
		if len(f.createdConn) != 1 || f.createdConn[0].Name != "trino-default-2" || f.createdConn[0].Type != "trino" ||
			f.createdConn[0].CreatedBy.Valid || string(f.createdConn[0].Config) != `{"url":"http://trino:8080"}` {
			t.Fatalf("connection = %+v", f.createdConn)
		}
		if len(f.engines) != 1 || f.engines[0].Kind != "trino" || f.engines[0].Class != "federated" || f.engines[0].IdentityMode != "impersonation" {
			t.Fatalf("engine = %+v", f.engines)
		}
		if len(f.links) != 1 || f.links[0].ID != f.cluster.ID {
			t.Fatalf("links = %+v", f.links)
		}
	})

	t.Run("updates the system engine's url", func(t *testing.T) {
		f := &fakeEngineStore{engine: defaultEngine("http://trino:8080", pgtype.UUID{})}
		if err := EnsureDefaultTrinoEngine(ctx, f, DefaultTrinoEngine{URL: "http://localhost:8088"}); err != nil {
			t.Fatal(err)
		}
		if len(f.updated) != 1 || *f.updated[0].Url != "http://localhost:8088" || string(f.updated[0].Config) != `{"url":"http://localhost:8088"}` {
			t.Fatalf("updates = %+v", f.updated)
		}
	})

	t.Run("no write when the url already matches", func(t *testing.T) {
		f := &fakeEngineStore{engine: defaultEngine("http://trino:8080", pgtype.UUID{})}
		if err := EnsureDefaultTrinoEngine(ctx, f, DefaultTrinoEngine{URL: "http://trino:8080"}); err != nil {
			t.Fatal(err)
		}
		if len(f.updated)+len(f.createdConn) != 0 {
			t.Fatal("unexpected write")
		}
	})

	t.Run("a user-owned connection is left alone", func(t *testing.T) {
		f := &fakeEngineStore{engine: defaultEngine("https://trino.prod", pgtype.UUID{Bytes: [16]byte{7}, Valid: true})}
		if err := EnsureDefaultTrinoEngine(ctx, f, DefaultTrinoEngine{URL: "http://localhost:8088"}); err != nil {
			t.Fatal(err)
		}
		if len(f.updated) != 0 {
			t.Fatal("TRINO_URL overwrote a connection a user manages")
		}
	})

	t.Run("no cluster, nothing to do", func(t *testing.T) {
		f := &fakeEngineStore{}
		if err := EnsureDefaultTrinoEngine(ctx, f, DefaultTrinoEngine{URL: "http://trino:8080"}); err != nil {
			t.Fatal(err)
		}
		if len(f.createdConn)+len(f.engines) != 0 {
			t.Fatal("created an engine without a cluster")
		}
	})
}

func TestEnsureDefaultTrinoEngine_Identities(t *testing.T) {
	f := &fakeEngineStore{engine: defaultEngine("http://trino:8080", pgtype.UUID{})}
	env := DefaultTrinoEngine{URL: "http://trino:8080", ServiceUser: "svc", AdminUser: "adm"}
	if err := EnsureDefaultTrinoEngine(context.Background(), f, env); err != nil {
		t.Fatal(err)
	}
	if len(f.updated) != 1 || string(f.updated[0].Config) != `{"admin_user":"adm","service_user":"svc","url":"http://trino:8080"}` {
		t.Fatalf("updates = %+v", f.updated)
	}
}
