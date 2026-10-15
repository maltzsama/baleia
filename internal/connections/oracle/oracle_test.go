package oracle

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
	goora "github.com/sijms/go-ora/v2"
)

// TestMetadataReader_oracle runs against a real server. Set
// BALEIA_TEST_ORACLE_URL (oracle://user:pass@host:port/SERVICE) to enable it;
// the user owns the fixture objects.
func TestMetadataReader_oracle(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	service := strings.TrimPrefix(u.Path, "/")
	user := u.User.Username()
	pass, _ := u.User.Password()
	ctx := context.Background()

	admin, err := sql.Open("oracle", goora.BuildUrl(host, port, service, user, pass, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	drop := func() {
		for _, s := range []string{`DROP VIEW big_orders`, `DROP TABLE orders`} {
			_, _ = admin.Exec(s)
		}
	}
	drop()
	for _, s := range []string{
		`CREATE TABLE orders (id NUMBER(19,0) NOT NULL, amount NUMBER(12,2), note VARCHAR2(200))`,
		`COMMENT ON TABLE orders IS 'fixture orders'`,
		`COMMENT ON COLUMN orders.note IS 'free text'`,
		`CREATE VIEW big_orders AS SELECT * FROM orders WHERE amount > 100`,
	} {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(drop)

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"host": host, "port": portStr, "service_name": service, "username": user},
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
	owner := strings.ToUpper(user)
	nss, err := r.ListNamespaces(ctx, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nss {
		found = found || n[0] == owner
		if n[0] == "SYS" || n[0] == "SYSTEM" {
			t.Errorf("Oracle-maintained schema listed: %s", n[0])
		}
	}
	if !found {
		t.Fatalf("namespaces = %v", nss)
	}
	tables, next, err := r.ListTables(ctx, service, connections.Namespace{owner}, "")
	if err != nil || next != "" || len(tables) != 2 || tables[0].Name != "BIG_ORDERS" || tables[0].Type != "VIEW" || tables[1].Type != "TABLE" {
		t.Fatalf("tables = %+v %q, %v", tables, next, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Comment != "fixture orders" || len(meta.Columns) != 3 || meta.Columns[0].Nullable ||
		meta.Columns[1].NativeType != "NUMBER(12,2)" || meta.Columns[2].NativeType != "VARCHAR2(200)" || meta.Columns[2].Comment != "free text" {
		t.Errorf("meta = %+v", meta)
	}
	conntest.CheckWalk(t, r, service, tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: service, Namespace: connections.Namespace{owner}, Name: "NOPE"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
}

// TestListTables_pagination: the second page starts after the token.
func TestListTables_pagination(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	u, _ := url.Parse(raw)
	host, port, _ := net.SplitHostPort(u.Host)
	pass, _ := u.User.Password()
	service := strings.TrimPrefix(u.Path, "/")
	d, _ := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	p := New(d)
	cfg, _ := p.Validate(connections.Config{
		Values:  map[string]string{"host": host, "port": port, "service_name": service, "username": u.User.Username()},
		Secrets: map[string]string{"password": pass},
	})
	r, err := p.OpenMetadata(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// SYS is not listed as a namespace but its tables are readable: a large set.
	first, _, err := r.ListTables(context.Background(), service, connections.Namespace{"SYS"}, "")
	if err != nil || len(first) < 2 {
		t.Fatalf("first page = %d, %v", len(first), err)
	}
	after, _, err := r.ListTables(context.Background(), service, connections.Namespace{"SYS"}, first[0].Name)
	if err != nil || len(after) == 0 || after[0].Name <= first[0].Name {
		t.Fatalf("page after %q starts at %v, %v", first[0].Name, after, err)
	}
}
