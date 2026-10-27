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

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs"
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
var storedKinds = []connections.Kind{
	"polaris", "nessie", "iceberg", "lakekeeper", "unity", "hive", "glue",
	"postgresql", "mysql", "mariadb", "sqlserver", "oracle", "sqlite", "clickhouse",
	"flightsql", "trino", "doris", "starrocks",
}

func TestEveryProviderIsComplete(t *testing.T) {
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})
	for _, p := range reg.All() {
		t.Run(string(p.Kind()), func(t *testing.T) {
			t.Parallel()
			if !slices.Contains(storedKinds, p.Kind()) {
				t.Errorf("kind %q is not allowed by catalog_connections.type", p.Kind())
			}
			if p.Label() == "" || p.Family() == "" || len(p.ConfigSchema()) == 0 {
				t.Fatal("label, family and schema are required")
			}
			// Roles may be empty: a metastore that is only a bindable source
			// (Hive Metastore, #86) declares none — "bindable" is declared by
			// binders, not providers.
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
			// The table keeps its ETag: a matching If-None-Match is a 304.
			if r.Header.Get("If-None-Match") == `W/"v2"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `W/"v2"`)
			write(w, map[string]any{"metadata-location": "s3://b/events/metadata/00002.metadata.json", "metadata": map[string]any{
				"format-version":      2,
				"location":            "s3://b/events",
				"current-schema-id":   1,
				"current-snapshot-id": 2,
				"schemas": []map[string]any{
					{"schema-id": 0, "fields": []map[string]any{{"id": 1, "name": "old", "type": "int", "required": true}}},
					{"schema-id": 1, "fields": []map[string]any{
						{"id": 1, "name": "id", "type": "long", "required": true},
						{"id": 2, "name": "tags", "type": map[string]any{"type": "list", "element": "string"}, "required": false, "doc": "labels"},
						{"id": 3, "name": "attrs", "type": map[string]any{"type": "map", "key": "string", "value": map[string]any{
							"type": "struct", "fields": []map[string]any{{"id": 5, "name": "n", "type": "long"}, {"id": 6, "name": "at", "type": "timestamptz"}},
						}}},
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
	t.Parallel()
	srv := fakeIcebergREST(t)
	t.Cleanup(srv.Close)
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
			t.Parallel()
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
			if len(meta.Columns) != 3 || meta.Columns[0].Name != "id" || meta.Columns[0].Nullable ||
				meta.Columns[1].NativeType != "list<string>" || meta.Columns[1].Comment != "labels" ||
				meta.Columns[2].NativeType != "map<string, struct<n: long, at: timestamptz>>" {
				t.Errorf("columns from the current schema = %+v", meta.Columns)
			}
			if len(meta.Snapshots) != 2 || meta.Snapshots[1].Operation != "overwrite" || meta.Properties["location"] != "s3://b/events" ||
				meta.Properties["metadata_location"] != "s3://b/events/metadata/00002.metadata.json" {
				t.Errorf("snapshots/properties = %+v %v", meta.Snapshots, meta.Properties)
			}

			// A conditional load of an unchanged table loads nothing.
			cl, ok := r.(connections.ConditionalLoader)
			if !ok {
				t.Fatal("Iceberg REST readers load conditionally")
			}
			if m, etag, err := cl.LoadTableIfChanged(ctx, tables[0], ""); err != nil || m == nil || etag != `W/"v2"` {
				t.Fatalf("first load = %v %q %v", m, etag, err)
			}
			if m, etag, err := cl.LoadTableIfChanged(ctx, tables[0], `W/"v2"`); err != nil || m != nil || etag != `W/"v2"` {
				t.Fatalf("unchanged load = %v %q %v", m, etag, err)
			}

			if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: c.catalog, Namespace: connections.Namespace{"db"}, Name: "missing"}); !errors.Is(err, connections.ErrNotFound) {
				t.Errorf("missing table: want ErrNotFound, got %v", err)
			}
		})
	}
}

func TestPolarisRejectsBadCredentials(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Default dialer: loopback is denied, so every provider must fail with
	// ErrDeniedNetwork before reaching anything.
	reg := registry(t, connections.DialerOptions{})
	configs := map[connections.Kind]connections.Config{
		connections.KindPolaris:       {Values: map[string]string{"url": "http://localhost:8181", "client_id": "a"}, Secrets: map[string]string{"client_secret": "b"}},
		connections.KindNessie:        {Values: map[string]string{"url": "http://localhost:19120"}},
		connections.KindIcebergREST:   {Values: map[string]string{"url": "http://localhost:8181/catalog"}},
		connections.KindHiveMetastore: {Values: map[string]string{"metastore_uri": "thrift://localhost:9083"}},
		connections.KindGlue:          {Values: map[string]string{"region": "us-east-1", "access_key": "a", "endpoint": "https://169.254.169.254:443"}, Secrets: map[string]string{"secret_key": "b"}},
		connections.KindLakekeeper:    {Values: map[string]string{"url": "http://localhost:8181"}},
		connections.KindUnity:         {Values: map[string]string{"url": "http://localhost:8080"}},
		connections.KindPostgreSQL:    {Values: map[string]string{"host": "localhost", "username": "u", "sslmode": "disable"}},
		connections.KindMySQL:         {Values: map[string]string{"host": "localhost", "username": "u", "tls": "false"}},
		connections.KindMariaDB:       {Values: map[string]string{"host": "localhost", "username": "u", "tls": "false"}},
		connections.KindSQLServer:     {Values: map[string]string{"host": "localhost", "username": "u", "encrypt": "disable"}},
		connections.KindOracle:        {Values: map[string]string{"host": "localhost", "service_name": "FREEPDB1", "username": "u"}},
		connections.KindClickHouse:    {Values: map[string]string{"host": "localhost"}},
		connections.KindFlightSQL:     {Values: map[string]string{"host": "localhost", "port": "32010", "tls": "false"}},
		connections.KindTrino:         {Values: map[string]string{"url": "http://localhost:8080"}},
	}
	// SQLite opens no network connection (TestSQLiteRootConfinement guards
	// it); planned kinds never connect.
	noNetwork := map[connections.Kind]bool{connections.KindSQLite: true}
	for _, p := range reg.All() {
		if p.Status().State == connections.StateUnsupported && p.Kind() != connections.KindSQLite {
			noNetwork[p.Kind()] = true
			if err := p.Ping(context.Background(), connections.Config{}); !errors.Is(err, connections.ErrUnsupported) {
				t.Errorf("%s: planned kind pinged: %v", p.Kind(), err)
			}
		}
	}
	for _, p := range reg.All() {
		if noNetwork[p.Kind()] {
			continue
		}
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
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})

	cfg, err := FromStored(reg, connections.KindPolaris, pgtype.UUID{}, "http://polaris:8181/", nil, []byte(`{"client_id":"id","client_secret":"sec","realm":"R","stray":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["url"] != "http://polaris:8181" || cfg.Values["client_id"] != "id" || cfg.Values["realm"] != "R" ||
		cfg.Secrets["client_secret"] != "sec" || len(cfg.Secrets) != 1 || cfg.Values["stray"] != "" {
		t.Errorf("polaris = %+v", cfg)
	}

	cfg, err = FromStored(reg, connections.KindPostgreSQL, pgtype.UUID{}, "jdbc:postgresql://db.internal:6543/sales", nil, []byte(`{"username":"u","password":"p"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["host"] != "db.internal" || cfg.Values["port"] != "6543" || cfg.Values["database"] != "sales" ||
		cfg.Values["username"] != "u" || cfg.Secrets["password"] != "p" {
		t.Errorf("postgresql = %+v", cfg)
	}

	if _, err := FromStored(reg, connections.Kind("duckdb"), pgtype.UUID{}, "", nil, nil); !errors.Is(err, connections.ErrUnknownKind) {
		t.Errorf("duckdb: want ErrUnknownKind, got %v", err)
	}
}

// TestFromStoredParseFailureIsUnreadable: a stored config or credentials blob
// that does not parse is a read failure of stored data, not a server bug —
// one errors.Is covers it, the way an unopenable envelope already does (#349).
func TestFromStoredParseFailureIsUnreadable(t *testing.T) {
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})

	cases := map[string]struct {
		config, creds []byte
	}{
		"corrupt config": {config: []byte(`[1,2,3]`)}, // valid JSON, not an object
		"corrupt creds":  {config: []byte(`{}`), creds: []byte(`not json`)},
	}
	for name, tc := range cases {
		got, err := FromStored(reg, connections.KindPolaris, pgtype.UUID{}, "http://polaris:8181/", tc.config, tc.creds)
		if !errors.Is(err, catalogs.ErrCredentialsUnreadable) {
			t.Errorf("%s: err = %v, want ErrCredentialsUnreadable", name, err)
		}
		if len(got.Values) > 0 || len(got.Secrets) > 0 {
			t.Errorf("%s: returned a config alongside an error: %+v", name, got)
		}
	}
}

func TestConnectionsMatrixUpToDate(t *testing.T) {
	t.Parallel()
	// The bindable column comes from the binders (bindings.TestSupportsMatrixMatchesDocs).
	got := Markdown(registry(t, connections.DialerOptions{}), bindtrino.New(nil, nil))
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

func TestFromStoredReadsConfigColumn(t *testing.T) {
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})

	// A database row after the migration backfill: the JDBC URL sits in config.
	cfg, err := FromStored(reg, connections.KindMySQL, pgtype.UUID{}, "jdbc:mysql://old:1/x", []byte(`{"url":"jdbc:mysql://db:3307/app"}`), []byte(`{"username":"u","password":"p"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["host"] != "db" || cfg.Values["port"] != "3307" || cfg.Values["database"] != "app" || cfg.Values["url"] != "" || cfg.Values["username"] != "u" {
		t.Errorf("mysql = %+v", cfg)
	}

	// A row written through the registry: config wins over a leftover blob value.
	cfg, err = FromStored(reg, connections.KindPolaris, pgtype.UUID{}, "http://ignored", []byte(`{"url":"http://p:8181","client_id":"new"}`), []byte(`{"client_id":"old","client_secret":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["url"] != "http://p:8181" || cfg.Values["client_id"] != "new" || cfg.Secrets["client_secret"] != "s" {
		t.Errorf("polaris = %+v", cfg)
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()
	reg := registry(t, connections.DialerOptions{})
	cur := connections.Config{Kind: connections.KindPostgreSQL,
		Values:  map[string]string{"host": "a", "port": "5432", "database": "d", "username": "u", "sslmode": "require"},
		Secrets: map[string]string{"password": "old"},
	}
	jdbc := "jdbc:postgresql://b:6543/e"
	got, err := Merge(reg, cur, &jdbc, map[string]string{"sslmode": ""}, map[string]any{"password": "", "username": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Values["host"] != "b" || got.Values["port"] != "6543" || got.Values["database"] != "e" || got.Values["username"] != "v" {
		t.Errorf("values = %v", got.Values)
	}
	if _, ok := got.Values["sslmode"]; ok {
		t.Error("an empty config value must clear the field")
	}
	if got.Secrets["password"] != "old" {
		t.Error("an empty secret must keep the stored one")
	}
	if cur.Values["host"] != "a" {
		t.Error("Merge modified its input")
	}
}

func TestLegacyURL(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cfg  connections.Config
		want string
	}{
		{connections.Config{Kind: connections.KindPolaris, Values: map[string]string{"url": "http://p"}}, "http://p"},
		{connections.Config{Kind: connections.KindPostgreSQL, Values: map[string]string{"host": "db", "port": "5432", "database": "s"}}, "jdbc:postgresql://db:5432/s"},
		{connections.Config{Kind: connections.KindMySQL, Values: map[string]string{"host": "::1", "port": "3306"}}, "jdbc:mysql://[::1]:3306"},
	} {
		if got := LegacyURL(c.cfg); got != c.want {
			t.Errorf("%s: %q, want %q", c.cfg.Kind, got, c.want)
		}
	}
}
