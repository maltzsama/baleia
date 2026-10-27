package lakekeeper

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	p := New(nil)

	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": "http://lakekeeper:8181/"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Value("url") != "http://lakekeeper:8181" || cfg.Value("auth_mode") != "none" {
		t.Errorf("validated config = %+v", cfg)
	}

	// oauth2 needs a client and a token endpoint.
	var ve *connections.ValidationError
	_, err = p.Validate(connections.Config{Values: map[string]string{"url": "http://lakekeeper:8181", "auth_mode": "oauth2"}})
	if !errors.As(err, &ve) || len(ve.Fields) != 3 {
		t.Fatalf("oauth2 without credentials: %v", err)
	}
	keys := map[string]bool{}
	for _, f := range ve.Fields {
		keys[f.Key] = true
	}
	for _, k := range []string{"client_id", "client_secret", "token_endpoint"} {
		if !keys[k] {
			t.Errorf("missing field error for %q", k)
		}
	}

	// oauth2 with everything validates.
	if _, err := p.Validate(connections.Config{
		Values:  map[string]string{"url": "http://lakekeeper:8181", "auth_mode": "oauth2", "client_id": "id", "token_endpoint": "https://idp/token"},
		Secrets: map[string]string{"client_secret": "sec"},
	}); err != nil {
		t.Errorf("complete oauth2 rejected: %v", err)
	}
}

func TestRESTURI(t *testing.T) {
	t.Parallel()
	p := New(nil)
	if got := p.RESTURI(connections.Config{Values: map[string]string{"url": "http://lk:8181/"}}); got != "http://lk:8181/catalog" {
		t.Errorf("RESTURI = %q", got)
	}
}

// TestLakekeeperIntegration runs against a real Lakekeeper (the compose
// `sources` service works: BALEIA_TEST_LAKEKEEPER_URL=http://localhost:18181
// with the seeded warehouse). Named without the TestMetadataReader_ prefix so
// the sources CI job does not require it when the env var is unset.
func TestLakekeeperIntegration(t *testing.T) {
	t.Parallel()
	base := os.Getenv("BALEIA_TEST_LAKEKEEPER_URL")
	if base == "" {
		t.Skip("BALEIA_TEST_LAKEKEEPER_URL not set")
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": base}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatalf("ping: %v", err)
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cats, err := r.ListCatalogs(ctx)
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	if len(cats) == 0 {
		t.Skip("lakekeeper has no warehouses to browse")
	}
	for _, c := range cats {
		if _, err := r.ListNamespaces(ctx, c.Name, nil); err != nil {
			t.Fatalf("%s: namespaces: %v", c.Name, err)
		}
		t.Logf("warehouse %s", c.Name)
	}
}
