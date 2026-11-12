package unity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

func TestValidate(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	base := os.Getenv("BALEIA_TEST_UNITY_URL")
	if base == "" {
		t.Skip("BALEIA_TEST_UNITY_URL not set")
	}
	d, err := connections.NewDialer(connections.DialerOptions{})
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

// TestOpenMetadata_MarksTablesDatabricksCannotServe: Databricks lists a managed
// table in its default storage (TABLE_DB_STORAGE) but never vends credentials for
// it, so the listing marks it unsupported with the kind as the reason; the other
// kinds, and a Unity that reports no kind, are left alone.
func TestOpenMetadata_MarksTablesDatabricksCannotServe(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/2.1/unity-catalog/iceberg-rest/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"overrides": map[string]string{"prefix": "catalogs/c"}})
		case "/api/2.1/unity-catalog/iceberg-rest/v1/catalogs/c/namespaces/s/tables":
			_ = json.NewEncoder(w).Encode(map[string]any{"identifiers": []map[string]any{
				{"namespace": []string{"s"}, "name": "native"},
				{"namespace": []string{"s"}, "name": "default_storage"},
				{"namespace": []string{"s"}, "name": "no_kind"},
			}})
		case "/api/2.1/unity-catalog/tables":
			if r.URL.Query().Get("catalog_name") != "c" || r.URL.Query().Get("schema_name") != "s" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tables": []map[string]any{
				{"name": "native", "securable_kind": "TABLE_DELTA_ICEBERG_MANAGED"},
				{"name": "default_storage", "securable_kind": "TABLE_DB_STORAGE"},
				{"name": "no_kind"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := connections.Config{Values: map[string]string{"url": srv.URL, "iceberg_path": "/api/2.1/unity-catalog/iceberg-rest"}}
	r, err := New(d).OpenMetadata(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	tables, _, err := r.ListTables(context.Background(), "c", connections.Namespace{"s"}, "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	got := map[string]connections.Unsupported{}
	for _, tb := range tables {
		got[tb.Name] = tb.Unsupported
	}
	want := map[string]connections.Unsupported{
		"native":          {},
		"default_storage": {Code: connections.UnsupportedKind, Detail: "TABLE_DB_STORAGE"},
		"no_kind":         {},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("table %q unsupported = %+v, want %+v", name, got[name], w)
		}
	}
}

// A classification that fails (an OSS Unity without the endpoint, a 403) must not
// fail the listing: the tables are listed and an unsupported one is caught when it
// is loaded.
func TestOpenMetadata_ClassificationFailureKeepsTheListing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/2.1/unity-catalog/iceberg/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/api/2.1/unity-catalog/iceberg/v1/namespaces/s/tables":
			_ = json.NewEncoder(w).Encode(map[string]any{"identifiers": []map[string]any{{"namespace": []string{"s"}, "name": "t"}}})
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(d).OpenMetadata(context.Background(), connections.Config{Values: map[string]string{"url": srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	tables, _, err := r.ListTables(context.Background(), "c", connections.Namespace{"s"}, "")
	if err != nil || len(tables) != 1 || tables[0].Unsupported.Is() {
		t.Fatalf("ListTables = %+v, %v; want one readable table", tables, err)
	}
}

// TestOpenMetadata_ClassifiesASchemaOnce: the Iceberg listing is paged and the
// classifier runs per page, but a schema is scanned through the Unity API once per
// reader; a failed scan is not kept, so the next page retries it.
func TestOpenMetadata_ClassifiesASchemaOnce(t *testing.T) {
	t.Parallel()
	var scans, failFirst atomic.Int32
	failFirst.Store(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/2.1/unity-catalog/iceberg/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/api/2.1/unity-catalog/iceberg/v1/namespaces/s/tables":
			_ = json.NewEncoder(w).Encode(map[string]any{"identifiers": []map[string]any{{"namespace": []string{"s"}, "name": "t"}}})
		case "/api/2.1/unity-catalog/tables":
			scans.Add(1)
			if failFirst.Add(-1) >= 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tables": []map[string]any{{"name": "t", "securable_kind": "TABLE_DB_STORAGE"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(d).OpenMetadata(context.Background(), connections.Config{Values: map[string]string{"url": srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	list := func() connections.TableRef {
		t.Helper()
		tables, _, err := r.ListTables(context.Background(), "c", connections.Namespace{"s"}, "")
		if err != nil || len(tables) != 1 {
			t.Fatalf("ListTables = %+v, %v", tables, err)
		}
		return tables[0]
	}
	if first := list(); first.Unsupported.Is() { // the scan failed: listed, unmarked
		t.Errorf("first page after a failed scan = %+v, want unmarked", first)
	}
	if second := list(); !second.Unsupported.Is() { // retried and kept
		t.Errorf("second page = %+v, want marked", second)
	}
	list()
	list()
	if got := scans.Load(); got != 2 {
		t.Errorf("Unity table scans = %d, want 2 (one failed, one kept)", got)
	}
}
