package polaris

import (
	"context"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

// TestMetadataReader_polaris runs against a real Polaris (the compose service
// works: BALEIA_TEST_POLARIS_URL=http://localhost:8181
// BALEIA_TEST_POLARIS_CREDENTIALS=admin:admin). It lists every catalog and
// walks the first level of each.
func TestMetadataReader_polaris(t *testing.T) {
	t.Parallel()
	base, creds := os.Getenv("BALEIA_TEST_POLARIS_URL"), os.Getenv("BALEIA_TEST_POLARIS_CREDENTIALS")
	if base == "" || creds == "" {
		t.Skip("BALEIA_TEST_POLARIS_URL / BALEIA_TEST_POLARIS_CREDENTIALS not set")
	}
	id, secret, _ := cut(creds)
	ctx := context.Background()

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{
		Values:  map[string]string{"url": base, "client_id": id},
		Secrets: map[string]string{"client_secret": secret},
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
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) == 0 {
		t.Skip("polaris has no catalogs to walk")
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
				meta, err := r.LoadTable(ctx, tb)
				if err != nil {
					t.Fatalf("%s.%v.%s: load: %v", c.Name, ns, tb.Name, err)
				}
				if len(meta.Columns) == 0 {
					t.Errorf("%s.%v.%s: no columns", c.Name, ns, tb.Name)
				}
			}
			t.Logf("%s.%v: %d tables", c.Name, ns, len(tables))
		}
	}

	bad := cfg
	bad.Secrets = map[string]string{"client_secret": "wrong"}
	if err := p.Ping(ctx, bad); err == nil {
		t.Error("ping accepted a wrong secret")
	}
}

func cut(s string) (string, string, bool) {
	for i := range s {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
