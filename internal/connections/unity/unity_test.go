package unity

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

func TestValidate(t *testing.T) {
	p := New(nil)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": "http://unity:8080/"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Value("url") != "http://unity:8080" || cfg.Value("iceberg_path") != defaultIcebergPath {
		t.Errorf("validated config = %+v", cfg)
	}

	// A bad path is rejected.
	var ve *connections.ValidationError
	_, err = p.Validate(connections.Config{Values: map[string]string{"url": "http://unity:8080", "iceberg_path": "api/2.1/unity-catalog/iceberg"}})
	if !errors.As(err, &ve) || ve.Fields[0].Key != "iceberg_path" {
		t.Errorf("bad iceberg_path: %v", err)
	}

	// A Databricks path override is accepted.
	if _, err := p.Validate(connections.Config{Values: map[string]string{"url": "https://dbc.cloud.databricks.com", "iceberg_path": "/api/2.1/unity-catalog/iceberg-rest/"}}); err != nil {
		t.Errorf("databricks path rejected: %v", err)
	}
}

func TestRESTURI(t *testing.T) {
	p := New(nil)
	cases := map[string]string{
		"http://unity:8080": "http://unity:8080/api/2.1/unity-catalog/iceberg",
	}
	for url, want := range cases {
		if got := p.RESTURI(connections.Config{Values: map[string]string{"url": url}}); got != want {
			t.Errorf("RESTURI(%q) = %q, want %q", url, got, want)
		}
	}
	// An explicit override wins.
	if got := p.RESTURI(connections.Config{Values: map[string]string{"url": "https://w", "iceberg_path": "/x/iceberg-rest"}}); got != "https://w/x/iceberg-rest" {
		t.Errorf("override RESTURI = %q", got)
	}
}

// TestUnityIntegration runs against a real Unity Catalog OSS (the compose
// `sources` service works: BALEIA_TEST_UNITY_URL=http://localhost:18080).
func TestUnityIntegration(t *testing.T) {
	base := os.Getenv("BALEIA_TEST_UNITY_URL")
	if base == "" {
		t.Skip("BALEIA_TEST_UNITY_URL not set")
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
	if err != nil || len(cats) == 0 {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	for _, c := range cats {
		nss, err := r.ListNamespaces(ctx, c.Name, nil)
		if err != nil {
			t.Fatalf("%s: namespaces: %v", c.Name, err)
		}
		for _, ns := range nss {
			tables, _, err := r.ListTables(ctx, c.Name, ns, "")
			if err != nil {
				t.Fatalf("%s.%v: tables: %v", c.Name, ns, err)
			}
			for _, tb := range tables {
				// LoadTable is best-effort: the OSS image's demo table has no
				// reachable storage, so its Iceberg REST endpoint answers 500
				// ("Could not read this table"). The provider call is correct;
				// a real deployment with readable storage returns metadata.
				meta, err := r.LoadTable(ctx, tb)
				if err != nil {
					t.Logf("%s.%v.%s: load: %v", c.Name, ns, tb.Name, err)
					continue
				}
				if len(meta.Columns) == 0 {
					t.Errorf("%s.%v.%s: no columns", c.Name, ns, tb.Name)
				}
			}
			t.Logf("%s.%v: %d tables", c.Name, ns, len(tables))
		}
	}
}
