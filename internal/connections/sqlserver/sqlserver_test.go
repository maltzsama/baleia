package sqlserver

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
	_ "github.com/microsoft/go-mssqldb"
)

// TestMetadataReader_sqlserver runs against a real server. Set
// BALEIA_TEST_SQLSERVER_URL (sqlserver://sa:pass@host:port) to enable it.
func TestMetadataReader_sqlserver(t *testing.T) {
	t.Parallel()
	raw := os.Getenv("BALEIA_TEST_SQLSERVER_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_SQLSERVER_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin, err := sql.Open("sqlserver", raw+"?encrypt=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, s := range []string{
		`IF DB_ID('baleia_fixture') IS NOT NULL BEGIN ALTER DATABASE baleia_fixture SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE baleia_fixture END`,
		`CREATE DATABASE baleia_fixture`,
		`USE baleia_fixture; EXEC('CREATE SCHEMA sales')`,
		`USE baleia_fixture; CREATE TABLE sales.orders (id bigint NOT NULL, amount decimal(12,2), note nvarchar(200))`,
		`USE baleia_fixture; EXEC sp_addextendedproperty 'MS_Description', 'free text', 'SCHEMA', 'sales', 'TABLE', 'orders', 'COLUMN', 'note'`,
		`USE baleia_fixture; EXEC('CREATE VIEW sales.big_orders AS SELECT * FROM sales.orders WHERE amount > 100')`,
	} {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`ALTER DATABASE baleia_fixture SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE baleia_fixture`)
	})

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	host, port, _ := net.SplitHostPort(u.Host)
	pass, _ := u.User.Password()
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"host": host, "port": port, "username": u.User.Username(), "encrypt": "disable"},
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

	cats, err := r.ListCatalogs(ctx)
	found := false
	for _, c := range cats {
		found = found || c.Name == "baleia_fixture"
		if c.Name == "master" || c.Name == "tempdb" {
			t.Errorf("system database listed: %s", c.Name)
		}
	}
	if err != nil || !found {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	nss, err := r.ListNamespaces(ctx, "baleia_fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	hasSales := false
	for _, n := range nss {
		hasSales = hasSales || n[0] == "sales"
	}
	if !hasSales {
		t.Fatalf("namespaces = %v", nss)
	}
	tables, next, err := r.ListTables(ctx, "baleia_fixture", connections.Namespace{"sales"}, "")
	if err != nil || next != "" || len(tables) != 2 || tables[0].Name != "big_orders" || tables[0].Type != "VIEW" {
		t.Fatalf("tables = %+v %q, %v", tables, next, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Columns) != 3 || meta.Columns[0].Nullable || meta.Columns[1].NativeType != "decimal(12,2)" ||
		meta.Columns[2].NativeType != "nvarchar(200)" || meta.Columns[2].Comment != "free text" {
		t.Errorf("columns = %+v", meta.Columns)
	}
	conntest.CheckWalk(t, r, "baleia_fixture", tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: "baleia_fixture", Namespace: connections.Namespace{"sales"}, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
	if _, err := r.ListNamespaces(ctx, "no_such_db", nil); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing database: %v", err)
	}
}
