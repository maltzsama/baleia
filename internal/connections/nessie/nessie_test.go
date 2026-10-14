package nessie

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

// TestMetadataReader_nessie runs against a real Nessie server with its Iceberg
// REST catalog enabled. Set BALEIA_TEST_NESSIE_URL (server root, e.g.
// http://localhost:19120) to enable it.
func TestMetadataReader_nessie(t *testing.T) {
	base := os.Getenv("BALEIA_TEST_NESSIE_URL")
	if base == "" {
		t.Skip("BALEIA_TEST_NESSIE_URL not set")
	}
	ctx := context.Background()

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	// Seed a namespace through the same REST API (prefix "main" = default
	// branch and warehouse). An existing namespace answers 409.
	resp, err := http.Post(p.RESTURI(cfg)+"/v1/main/namespaces", "application/json",
		strings.NewReader(`{"namespace":["baleia_fixture"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		t.Fatalf("create namespace: %d", resp.StatusCode)
	}

	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cats, err := r.ListCatalogs(ctx)
	if err != nil || len(cats) != 1 {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	nss, err := r.ListNamespaces(ctx, cats[0].Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nss {
		found = found || (len(n) == 1 && n[0] == "baleia_fixture")
	}
	if !found {
		t.Fatalf("namespaces = %v", nss)
	}
	tables, _, err := r.ListTables(ctx, cats[0].Name, connections.Namespace{"baleia_fixture"}, "")
	if err != nil || len(tables) != 0 {
		t.Fatalf("tables = %v, %v", tables, err)
	}
}
