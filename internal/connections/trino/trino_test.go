package trino

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

func provider(t *testing.T) *Provider {
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return New(d)
}

func TestValidate(t *testing.T) {
	p := provider(t)
	for _, c := range []connections.Config{
		{Values: map[string]string{"url": "http://trino:8080", "username": "u"}, Secrets: map[string]string{"password": "p"}},
		{Values: map[string]string{"url": "https://trino:8443", "username": "u"}},
		{Values: map[string]string{"url": "https://trino:8443"}, Secrets: map[string]string{"password": "p"}},
	} {
		if _, err := p.Validate(c); !errors.Is(err, connections.ErrValidation) {
			t.Errorf("%v accepted: %v", c.Values, err)
		}
	}
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": "https://trino:8443/", "username": "u"}, Secrets: map[string]string{"password": "p"}})
	if err != nil || cfg.Values["url"] != "https://trino:8443" || cfg.Values["service_user"] != "baleia" {
		t.Errorf("cfg = %+v, %v", cfg, err)
	}
}

// TestMetadataReader_trino runs against a real coordinator. Set
// BALEIA_TEST_TRINO_URL (http://host:port) to enable it; it reads the
// built-in system catalog's metadata schema, present on every server.
func TestMetadataReader_trino(t *testing.T) {
	url := os.Getenv("BALEIA_TEST_TRINO_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_TRINO_URL not set")
	}
	p := provider(t)
	ctx := context.Background()
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": url}})
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
	if _, err := r.ListCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	nss, err := r.ListNamespaces(ctx, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nss {
		found = found || n[0] == "metadata"
	}
	if !found {
		t.Fatalf("system namespaces = %v", nss)
	}
	tables, _, err := r.ListTables(ctx, "system", connections.Namespace{"metadata"}, "")
	if err != nil || len(tables) == 0 {
		t.Fatalf("tables = %v, %v", tables, err)
	}
	meta, err := r.LoadTable(ctx, connections.TableRef{Catalog: "system", Namespace: connections.Namespace{"metadata"}, Name: "catalogs"})
	if err != nil || len(meta.Columns) == 0 || meta.Columns[0].Name != "catalog_name" || meta.Columns[0].Position != 1 {
		t.Fatalf("catalogs = %+v, %v", meta, err)
	}
	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: "system", Namespace: connections.Namespace{"metadata"}, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
	if _, err := r.ListNamespaces(ctx, "no_such_catalog", nil); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing catalog: %v", err)
	}
}
