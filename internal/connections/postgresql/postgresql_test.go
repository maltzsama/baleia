package postgresql

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
	"github.com/jackc/pgx/v5"
)

// TestMetadataReader_postgresql runs against a real server. Set
// BALEIA_TEST_POSTGRES_URL (postgres://user:pass@host:port/db) to enable it;
// the user needs CREATE on the database.
func TestMetadataReader_postgresql(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_POSTGRES_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS baleia_fixture CASCADE`,
		`CREATE SCHEMA baleia_fixture`,
		`CREATE TABLE baleia_fixture.orders (id bigint NOT NULL, amount numeric(12,2), note text)`,
		`COMMENT ON TABLE baleia_fixture.orders IS 'fixture orders'`,
		`COMMENT ON COLUMN baleia_fixture.orders.note IS 'free text'`,
		`CREATE VIEW baleia_fixture.big_orders AS SELECT * FROM baleia_fixture.orders WHERE amount > 100`,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS baleia_fixture CASCADE`) })

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	pass, _ := u.User.Password()
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"host": u.Hostname(), "port": u.Port(), "database": u.Path[1:], "username": u.User.Username(), "sslmode": "disable"},
		Secrets: map[string]string{"password": pass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	db := cfg.Values["database"]
	cats, err := r.ListCatalogs(ctx)
	if err != nil || !hasCatalog(cats, db) {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	nss, err := r.ListNamespaces(ctx, db, nil)
	if err != nil || !hasNamespace(nss, "baleia_fixture") {
		t.Fatalf("namespaces = %v, %v", nss, err)
	}
	tables, next, err := r.ListTables(ctx, db, connections.Namespace{"baleia_fixture"}, "")
	if err != nil || next != "" || len(tables) != 2 || tables[0].Name != "big_orders" || tables[0].Type != "VIEW" || tables[1].Type != "TABLE" {
		t.Fatalf("tables = %+v %q, %v", tables, next, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Comment != "fixture orders" || len(meta.Columns) != 3 ||
		meta.Columns[0].Nullable || meta.Columns[1].NativeType != "numeric(12,2)" || meta.Columns[2].Comment != "free text" {
		t.Errorf("meta = %+v", meta)
	}
	conntest.CheckWalk(t, r, db, tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: db, Namespace: connections.Namespace{"baleia_fixture"}, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}

	// Sessions are read-only.
	reader := r.(*reader)
	c, _ := reader.conn(ctx, db)
	if _, err := c.Exec(ctx, `CREATE TABLE baleia_fixture.nope (id int)`); err == nil {
		t.Error("metadata session accepted a write")
	}
}

func hasCatalog(cs []connections.CatalogRef, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func hasNamespace(ns []connections.Namespace, name string) bool {
	for _, n := range ns {
		if len(n) == 1 && n[0] == name {
			return true
		}
	}
	return false
}
