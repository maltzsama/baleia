package clickhouse

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
)

// TestMetadataReader_clickhouse runs against a real server. Set
// BALEIA_TEST_CLICKHOUSE_URL (http://user:pass@host:port) to enable it.
func TestMetadataReader_clickhouse(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_CLICKHOUSE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_CLICKHOUSE_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	exec := func(sql string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}).String(), strings.NewReader(sql))
		req.SetBasicAuth(u.User.Username(), pass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: HTTP %d", sql, resp.StatusCode)
		}
	}
	exec(`DROP DATABASE IF EXISTS baleia_fixture`)
	exec(`CREATE DATABASE baleia_fixture`)
	exec(`CREATE TABLE baleia_fixture.orders (id UInt64, amount Decimal(12,2), note Nullable(String) COMMENT 'free text') ENGINE = MergeTree ORDER BY id COMMENT 'fixture orders'`)
	exec(`CREATE VIEW baleia_fixture.big_orders AS SELECT * FROM baleia_fixture.orders WHERE amount > 100`)
	t.Cleanup(func() { exec(`DROP DATABASE IF EXISTS baleia_fixture`) })

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	host, port, _ := net.SplitHostPort(u.Host)
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"host": host, "port": port, "username": u.User.Username()},
		Secrets: map[string]string{"password": pass},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	nss, err := r.ListNamespaces(ctx, Catalog, nil)
	found := false
	for _, n := range nss {
		found = found || n[0] == "baleia_fixture"
		if n[0] == "system" {
			t.Error("system database listed")
		}
	}
	if err != nil || !found {
		t.Fatalf("namespaces = %v, %v", nss, err)
	}
	tables, next, err := r.ListTables(ctx, Catalog, connections.Namespace{"baleia_fixture"}, "")
	if err != nil || next != "" || len(tables) != 2 || tables[0].Name != "big_orders" || tables[0].Type != "VIEW" || tables[1].Type != "TABLE" {
		t.Fatalf("tables = %+v %q, %v", tables, next, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Comment != "fixture orders" || meta.Format != "mergetree" || len(meta.Columns) != 3 ||
		meta.Columns[1].NativeType != "Decimal(12, 2)" || !meta.Columns[2].Nullable || meta.Columns[2].Comment != "free text" {
		t.Errorf("meta = %+v", meta)
	}
	conntest.CheckWalk(t, r, Catalog, tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{"baleia_fixture"}, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
	if _, _, err := r.ListTables(ctx, Catalog, connections.Namespace{"no_such_db"}, ""); err != nil {
		t.Errorf("an unknown database lists no tables, got %v", err)
	}

	// Sessions are read-only.
	if _, err := r.(*reader).c.query(ctx, "CREATE TABLE baleia_fixture.x (a UInt8) ENGINE = Memory", nil); err == nil {
		t.Error("metadata session accepted a write")
	}
}
