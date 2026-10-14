package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/jackc/pgx/v5/pgtype"
)

func registry(t *testing.T, o connections.DialerOptions) *connections.Registry {
	t.Helper()
	d, err := connections.NewDialer(o)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// storedKinds is the catalog_connections.type CHECK of the current migrations.
var storedKinds = []connections.Kind{"polaris", "nessie", "glue", "hive", "iceberg", "postgresql", "mysql"}

func TestEveryProviderIsComplete(t *testing.T) {
	reg := registry(t, connections.DialerOptions{})
	for _, p := range reg.All() {
		t.Run(string(p.Kind()), func(t *testing.T) {
			if !slices.Contains(storedKinds, p.Kind()) {
				t.Errorf("kind %q is not allowed by catalog_connections.type", p.Kind())
			}
			if p.Label() == "" || p.Family() == "" || len(p.Roles()) == 0 || len(p.ConfigSchema()) == 0 {
				t.Fatal("label, family, roles and schema are required")
			}
			if connections.HasRole(p, connections.RoleBindable) {
				t.Error("bindable is declared by binders, not providers")
			}
			if (connections.HasRole(p, connections.RoleDirect) || connections.HasRole(p, connections.RoleFederated)) != (p.EngineKind() != "") {
				t.Error("engine kind must be set exactly when the provider is an engine")
			}
			keys := map[string]bool{}
			for _, f := range p.ConfigSchema() {
				if keys[f.Key] {
					t.Errorf("duplicate field %q", f.Key)
				}
				keys[f.Key] = true
			}

			_, err := p.Validate(connections.Config{Kind: p.Kind(), Values: map[string]string{"bogus": "x"}})
			var ve *connections.ValidationError
			if !errors.As(err, &ve) || !slices.ContainsFunc(ve.Fields, func(f connections.FieldError) bool { return f.Key == "bogus" }) {
				t.Errorf("unknown key not rejected: %v", err)
			}
		})
	}
}

// fakeIcebergREST serves the Iceberg REST endpoints the readers call, under
// every base path a provider may use, plus Polaris' management API.
func fakeIcebergREST(t *testing.T) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		if strings.HasSuffix(path, "/v1/oauth/tokens") {
			if r.FormValue("client_secret") != "s3cret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			write(w, map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		nessie := strings.HasPrefix(path, "/iceberg/")
		for _, base := range []string{"/api/catalog", "/iceberg", "/rest"} {
			path = strings.TrimPrefix(path, base)
		}
		// Nessie sends an already escaped prefix in defaults; the client must
		// put it in the path unchanged.
		path = strings.Replace(path, "/v1/main%7Cwh/", "/v1/p-wh/", 1)
		switch path {
		case "/api/management/v1/catalogs":
			if r.Header.Get("Polaris-Realm") != "POLARIS" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			write(w, map[string]any{"catalogs": []map[string]string{{"name": "wh", "type": "INTERNAL"}}})
		case "/v1/config":
			if nessie {
				write(w, map[string]any{"defaults": map[string]string{"prefix": "main%7C" + r.URL.Query().Get("warehouse")}})
				return
			}
			write(w, map[string]any{
				"defaults":  map[string]string{"prefix": "ignored"},
				"overrides": map[string]string{"prefix": "p-" + r.URL.Query().Get("warehouse")},
			})
		case "/v1/p-wh/namespaces", "/v1/p-/namespaces":
			if r.URL.Query().Get("parent") == "" {
				write(w, map[string]any{"namespaces": [][]string{{"db"}}})
			} else {
				write(w, map[string]any{"namespaces": [][]string{{"db", "inner"}}})
			}
		case "/v1/p-wh/namespaces/db%1Finner/tables", "/v1/p-/namespaces/db%1Finner/tables":
			write(w, map[string]any{"identifiers": []map[string]any{{"namespace": []string{"db", "inner"}, "name": "events"}}})
		case "/v1/p-wh/namespaces/db%1Finner/tables/events", "/v1/p-/namespaces/db%1Finner/tables/events":
			write(w, map[string]any{"metadata": map[string]any{
				"format-version":      2,
				"location":            "s3://b/events",
				"current-schema-id":   1,
				"current-snapshot-id": 2,
				"schemas": []map[string]any{
					{"schema-id": 0, "fields": []map[string]any{{"id": 1, "name": "old", "type": "int", "required": true}}},
					{"schema-id": 1, "fields": []map[string]any{
						{"id": 1, "name": "id", "type": "long", "required": true},
						{"id": 2, "name": "tags", "type": map[string]any{"type": "list", "element": "string"}, "required": false, "doc": "labels"},
					}},
				},
				"snapshots": []map[string]any{
					{"snapshot-id": 1, "timestamp-ms": 10, "summary": map[string]string{"operation": "append"}},
					{"snapshot-id": 2, "parent-snapshot-id": 1, "timestamp-ms": 20, "summary": map[string]string{"operation": "overwrite", "total-data-files": "3"}},
				},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestIcebergRESTReaders(t *testing.T) {
	srv := fakeIcebergREST(t)
	defer srv.Close()
	reg := registry(t, connections.DialerOptions{AllowLoopback: true})

	cases := []struct {
		kind    connections.Kind
		values  map[string]string
		secrets map[string]string
		catalog string
	}{
		{connections.KindPolaris, map[string]string{"url": srv.URL, "client_id": "id"}, map[string]string{"client_secret": "s3cret"}, "wh"},
		{connections.KindNessie, map[string]string{"url": srv.URL, "warehouse": "wh"}, map[string]string{"token": "tok"}, "wh"},
		{connections.KindIcebergREST, map[string]string{"url": srv.URL + "/rest", "client_id": "id"}, map[string]string{"client_secret": "s3cret"}, "default"},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			ctx := context.Background()
			p, err := reg.Get(c.kind)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := p.Validate(connections.Config{Kind: c.kind, Values: c.values, Secrets: c.secrets})
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Ping(ctx, cfg); err != nil {
				t.Fatalf("ping: %v", err)
			}
			r, err := p.OpenMetadata(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			cats, err := r.ListCatalogs(ctx)
			if err != nil || len(cats) != 1 || cats[0].Name != c.catalog {
				t.Fatalf("catalogs = %v, %v", cats, err)
			}
			top, err := r.ListNamespaces(ctx, c.catalog, nil)
			if err != nil || len(top) != 1 || top[0][0] != "db" {
				t.Fatalf("namespaces = %v, %v", top, err)
			}
			nested, err := r.ListNamespaces(ctx, c.catalog, connections.Namespace{"db"})
			if err != nil || len(nested) != 1 || len(nested[0]) != 2 {
				t.Fatalf("nested namespaces = %v, %v", nested, err)
			}
			tables, next, err := r.ListTables(ctx, c.catalog, nested[0], "")
			if err != nil || next != "" || len(tables) != 1 || tables[0].Name != "events" {
				t.Fatalf("tables = %v %q, %v", tables, next, err)
			}
			meta, err := r.LoadTable(ctx, tables[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(meta.Columns) != 2 || meta.Columns[0].Name != "id" || meta.Columns[0].Nullable ||
				meta.Columns[1].NativeType != "list" || meta.Columns[1].Comment != "labels" {
				t.Errorf("columns from the current schema = %+v", meta.Columns)
			}
			if len(meta.Snapshots) != 2 || meta.Snapshots[1].Operation != "overwrite" || meta.Properties["location"] != "s3://b/events" {
				t.Errorf("snapshots/properties = %+v %v", meta.Snapshots, meta.Properties)
			}

			if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: c.catalog, Namespace: connections.Namespace{"db"}, Name: "missing"}); !errors.Is(err, connections.ErrNotFound) {
				t.Errorf("missing table: want ErrNotFound, got %v", err)
			}
		})
	}
}

func TestPolarisRejectsBadCredentials(t *testing.T) {
	srv := fakeIcebergREST(t)
	defer srv.Close()
	reg := registry(t, connections.DialerOptions{AllowLoopback: true})
	p, _ := reg.Get(connections.KindPolaris)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"url": srv.URL, "client_id": "id"}, Secrets: map[string]string{"client_secret": "wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	err = p.Ping(context.Background(), cfg)
	if err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("want an auth error without the secret, got %v", err)
	}
}

func TestRESTURIs(t *testing.T) {
	reg := registry(t, connections.DialerOptions{})
	cfg := connections.Config{Values: map[string]string{"url": "http://host:1/base/"}}
	want := map[connections.Kind]string{
		connections.KindPolaris:     "http://host:1/base/api/catalog",
		connections.KindNessie:      "http://host:1/base/iceberg",
		connections.KindIcebergREST: "http://host:1/base",
	}
	for kind, uri := range want {
		p, _ := reg.Get(kind)
		ep, ok := p.(connections.IcebergRESTEndpoint)
		if !ok {
			t.Fatalf("%s does not expose its REST URI", kind)
		}
		if got := ep.RESTURI(cfg); got != uri {
			t.Errorf("%s: RESTURI = %q, want %q", kind, got, uri)
		}
	}
}

func TestEveryDriverUsesDialer(t *testing.T) {
	// Default dialer: loopback is denied, so every provider must fail with
	// ErrDeniedNetwork before reaching anything.
	reg := registry(t, connections.DialerOptions{})
	configs := map[connections.Kind]connections.Config{
		connections.KindPolaris:     {Values: map[string]string{"url": "http://localhost:8181", "client_id": "a"}, Secrets: map[string]string{"client_secret": "b"}},
		connections.KindNessie:      {Values: map[string]string{"url": "http://localhost:19120"}},
		connections.KindIcebergREST: {Values: map[string]string{"url": "http://localhost:8181/catalog"}},
		connections.KindPostgreSQL:  {Values: map[string]string{"host": "localhost", "username": "u", "sslmode": "disable"}},
		connections.KindMySQL:       {Values: map[string]string{"host": "localhost", "username": "u", "tls": "false"}},
	}
	for _, p := range reg.All() {
		raw, ok := configs[p.Kind()]
		if !ok {
			t.Errorf("%s: no dialer test config", p.Kind())
			continue
		}
		raw.Kind = p.Kind()
		cfg, err := p.Validate(raw)
		if err != nil {
			t.Fatalf("%s: %v", p.Kind(), err)
		}
		if err := p.Ping(context.Background(), cfg); !errors.Is(err, connections.ErrDeniedNetwork) {
			t.Errorf("%s: want ErrDeniedNetwork, got %v", p.Kind(), err)
		}
	}
}

func TestFromStored(t *testing.T) {
	reg := registry(t, connections.DialerOptions{})

	cfg, err := FromStored(reg, connections.KindPolaris, pgtype.UUID{}, "http://polaris:8181/", []byte(`{"client_id":"id","client_secret":"sec","realm":"R","stray":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["url"] != "http://polaris:8181" || cfg.Values["client_id"] != "id" || cfg.Values["realm"] != "R" ||
		cfg.Secrets["client_secret"] != "sec" || len(cfg.Secrets) != 1 || cfg.Values["stray"] != "" {
		t.Errorf("polaris = %+v", cfg)
	}

	cfg, err = FromStored(reg, connections.KindPostgreSQL, pgtype.UUID{}, "jdbc:postgresql://db.internal:6543/sales", []byte(`{"username":"u","password":"p"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["host"] != "db.internal" || cfg.Values["port"] != "6543" || cfg.Values["database"] != "sales" ||
		cfg.Values["username"] != "u" || cfg.Secrets["password"] != "p" {
		t.Errorf("postgresql = %+v", cfg)
	}

	if _, err := FromStored(reg, connections.KindGlue, pgtype.UUID{}, "", nil); !errors.Is(err, connections.ErrUnknownKind) {
		t.Errorf("glue: want ErrUnknownKind, got %v", err)
	}
}

func TestConnectionsMatrixUpToDate(t *testing.T) {
	got := Markdown(registry(t, connections.DialerOptions{}))
	const path = "../../../docs/connections.md"
	if os.Getenv("BALEIA_UPDATE_DOCS") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (generate it with BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/)", err)
	}
	if string(want) != got {
		t.Error("docs/connections.md is stale; regenerate it with BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/")
	}
}
