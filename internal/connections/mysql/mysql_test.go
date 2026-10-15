package mysql

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
	driver "github.com/go-sql-driver/mysql"
)

// TestMetadataReader_mysql runs against a real server. Set
// BALEIA_TEST_MYSQL_URL (mysql://user:pass@host:port) to enable it; the user
// needs CREATE and DROP on databases.
func TestMetadataReader_mysql(t *testing.T) {
	runMetadataReader(t, "BALEIA_TEST_MYSQL_URL", New)
}

// TestMetadataReader_mariadb is the same suite against MariaDB
// (BALEIA_TEST_MARIADB_URL).
func TestMetadataReader_mariadb(t *testing.T) {
	runMetadataReader(t, "BALEIA_TEST_MARIADB_URL", NewMariaDB)
}

func runMetadataReader(t *testing.T, env string, newProvider func(*connections.Dialer) *Provider) {
	raw := os.Getenv(env)
	if raw == "" {
		t.Skip(env + " not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	ctx := context.Background()

	mc := driver.NewConfig()
	mc.Net, mc.Addr, mc.User, mc.Passwd = "tcp", u.Host, u.User.Username(), pass
	admin, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS baleia_fixture`,
		`CREATE DATABASE baleia_fixture`,
		`CREATE TABLE baleia_fixture.orders (id BIGINT NOT NULL, amount DECIMAL(12,2), note TEXT COMMENT 'free text') ENGINE=InnoDB COMMENT='fixture orders'`,
		`CREATE VIEW baleia_fixture.big_orders AS SELECT * FROM baleia_fixture.orders WHERE amount > 100`,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE IF EXISTS baleia_fixture`) })

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := newProvider(d)
	host, port, _ := net.SplitHostPort(u.Host)
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"host": host, "port": port, "username": u.User.Username(), "tls": "false"},
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

	nss, err := r.ListNamespaces(ctx, Catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nss {
		if n[0] == "baleia_fixture" {
			found = true
		}
		if n[0] == "mysql" || n[0] == "information_schema" {
			t.Errorf("system schema listed: %v", n)
		}
	}
	if !found {
		t.Fatalf("namespaces = %v", nss)
	}
	tables, next, err := r.ListTables(ctx, Catalog, connections.Namespace{"baleia_fixture"}, "")
	if err != nil || next != "" || len(tables) != 2 || tables[0].Type != "VIEW" || tables[1].Type != "TABLE" {
		t.Fatalf("tables = %+v %q, %v", tables, next, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Comment != "fixture orders" || meta.Format != "innodb" || len(meta.Columns) != 3 ||
		meta.Columns[0].Nullable || meta.Columns[1].NativeType != "decimal(12,2)" || meta.Columns[2].Comment != "free text" {
		t.Errorf("meta = %+v", meta)
	}
	conntest.CheckWalk(t, r, Catalog, tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{"baleia_fixture"}, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}

	// Sessions are read-only.
	if _, err := r.(*reader).db.ExecContext(ctx, `CREATE TABLE baleia_fixture.nope (id INT)`); err == nil {
		t.Error("metadata session accepted a write")
	}
}
