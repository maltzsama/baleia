package trino

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func cfg(kind connections.Kind, values, secrets map[string]string) connections.Config {
	return connections.Config{Kind: kind, Values: values, Secrets: secrets}
}

// TestProject_PolarisBuildsPolarisShape: the Polaris projection is
// byte-identical to the one before the binder, so no Polaris catalog has to
// resync; the version reproduces the value captured from Trino 482.
func TestProject_PolarisBuildsPolarisShape(t *testing.T) {
	t.Parallel()
	src := cfg(connections.KindPolaris, map[string]string{"url": "http://polaris:8181", "client_id": "abc", "realm": "POLARIS"}, map[string]string{"client_secret": "def"})
	p, err := New(slog.New(slog.DiscardHandler), nil, nil).Project(bindings.BindingSpec{EngineCatalog: "baleia_polaris", SourceRef: "baleia"}, src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"iceberg.catalog.type":                            "rest",
		"iceberg.rest-catalog.uri":                        "http://polaris:8181/api/catalog",
		"iceberg.rest-catalog.warehouse":                  "baleia",
		"iceberg.rest-catalog.security":                   "OAUTH2",
		"iceberg.rest-catalog.oauth2.credential":          "abc:def",
		"iceberg.rest-catalog.oauth2.scope":               "PRINCIPAL_ROLE:ALL",
		"iceberg.rest-catalog.vended-credentials-enabled": "true",
		"fs.s3.enabled":                                   "true",
	}
	if !mapsEqual(p.Properties, want) {
		t.Errorf("properties = %v", p.Properties)
	}
	if p.Connector != "iceberg" || !slices.Equal(p.SecretKeys, []string{"iceberg.rest-catalog.oauth2.credential"}) {
		t.Errorf("connector %q, secrets %v", p.Connector, p.SecretKeys)
	}
	if p.Version != "d0e1bd0910fac8707c67296aa19bffbfa2740e4bbe578b4b15aa29897a9aa2a9" {
		t.Errorf("version = %s; the Polaris projection changed", p.Version)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestProject_Kinds fixes the projection of every bindable kind.
func TestProject_Kinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		src     connections.Config
		spec    bindings.BindingSpec
		want    map[string]string
		secrets []string
	}{
		{
			name: "nessie without auth",
			src:  cfg(connections.KindNessie, map[string]string{"url": "http://nessie:19120"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "lake", SourceRef: "wh"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://nessie:19120/iceberg",
				"iceberg.rest-catalog.warehouse": "wh", "iceberg.rest-catalog.security": "NONE", "fs.s3.enabled": "true",
			},
		},
		{
			name: "nessie with a bearer token",
			src:  cfg(connections.KindNessie, map[string]string{"url": "http://nessie:19120", "warehouse": "wh"}, map[string]string{"token": "tok"}),
			spec: bindings.BindingSpec{EngineCatalog: "lake"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://nessie:19120/iceberg",
				"iceberg.rest-catalog.warehouse": "wh", "iceberg.rest-catalog.security": "OAUTH2",
				"iceberg.rest-catalog.oauth2.token": "tok", "fs.s3.enabled": "true",
			},
			secrets: []string{"iceberg.rest-catalog.oauth2.token"},
		},
		{
			name: "iceberg rest with client credentials",
			src: cfg(connections.KindIcebergREST, map[string]string{"url": "https://rest.example/catalog/", "client_id": "id", "scope": "catalog", "oauth2_server_uri": "https://auth.example/token"},
				map[string]string{"client_secret": "sec"}),
			spec: bindings.BindingSpec{EngineCatalog: "rest", SourceRef: "w"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "https://rest.example/catalog",
				"iceberg.rest-catalog.warehouse": "w", "iceberg.rest-catalog.security": "OAUTH2",
				"iceberg.rest-catalog.oauth2.credential": "id:sec", "iceberg.rest-catalog.oauth2.scope": "catalog",
				"iceberg.rest-catalog.oauth2.server-uri": "https://auth.example/token", "fs.s3.enabled": "true",
			},
			secrets: []string{"iceberg.rest-catalog.oauth2.credential"},
		},
		{
			name: "postgresql: the JDBC URL is built from fields",
			src: cfg(connections.KindPostgreSQL, map[string]string{"host": "db.example", "port": "6432", "database": "app", "sslmode": "require", "username": "u"},
				map[string]string{"password": "pw"}),
			spec: bindings.BindingSpec{EngineCatalog: "app"},
			want: map[string]string{
				"connection-url":      "jdbc:postgresql://db.example:6432/app?sslmode=require",
				"connection-user":     "u",
				"connection-password": "pw",
			},
			secrets: []string{"connection-password"},
		},
		{
			name: "postgresql: the binding's source ref picks the database",
			src:  cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "database": "app", "username": "u"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "other", SourceRef: "reports"},
			want: map[string]string{"connection-url": "jdbc:postgresql://db:5432/reports", "connection-user": "u"},
		},
		{
			name: "mysql",
			src:  cfg(connections.KindMySQL, map[string]string{"host": "my", "tls": "true", "username": "u"}, map[string]string{"password": "pw"}),
			spec: bindings.BindingSpec{EngineCatalog: "shop"},
			want: map[string]string{
				"connection-url": "jdbc:mysql://my:3306?sslMode=VERIFY_IDENTITY", "connection-user": "u", "connection-password": "pw",
			},
			secrets: []string{"connection-password"},
		},
		{
			name: "mariadb",
			src:  cfg(connections.KindMariaDB, map[string]string{"host": "ma", "tls": "true", "username": "u"}, map[string]string{"password": "pw"}),
			spec: bindings.BindingSpec{EngineCatalog: "shop"},
			want: map[string]string{
				"connection-url": "jdbc:mariadb://ma:3306?sslMode=verify-full", "connection-user": "u", "connection-password": "pw",
			},
			secrets: []string{"connection-password"},
		},
		{
			name: "sqlserver",
			src: cfg(connections.KindSQLServer, map[string]string{"host": "ms", "database": "sales", "encrypt": "disable", "trust_server_certificate": "true", "username": "sa"},
				map[string]string{"password": "pw"}),
			spec: bindings.BindingSpec{EngineCatalog: "sales"},
			want: map[string]string{
				"connection-url":  "jdbc:sqlserver://ms:1433;databaseName=sales;encrypt=false;trustServerCertificate=true",
				"connection-user": "sa", "connection-password": "pw",
			},
			secrets: []string{"connection-password"},
		},
		{
			name: "oracle: the binding's source ref is the service",
			src:  cfg(connections.KindOracle, map[string]string{"host": "ora", "port": "1522", "service_name": "FREEPDB1", "username": "app"}, map[string]string{"password": "pw"}),
			spec: bindings.BindingSpec{EngineCatalog: "erp", SourceRef: "ERP.example"},
			want: map[string]string{
				"connection-url": "jdbc:oracle:thin:@//ora:1522/ERP.example", "connection-user": "app", "connection-password": "pw",
			},
			secrets: []string{"connection-password"},
		},
		{
			name: "lakekeeper without auth",
			src:  cfg(connections.KindLakekeeper, map[string]string{"url": "http://lk:8181"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "lake", SourceRef: "wh"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://lk:8181/catalog",
				"iceberg.rest-catalog.warehouse": "wh", "iceberg.rest-catalog.vended-credentials-enabled": "true", "fs.s3.enabled": "true",
			},
		},
		{
			name: "lakekeeper with oauth2",
			src: cfg(connections.KindLakekeeper, map[string]string{"url": "http://lk:8181/", "auth_mode": "oauth2", "client_id": "id", "token_endpoint": "https://idp.example/token", "scope": "catalog"},
				map[string]string{"client_secret": "sec"}),
			spec: bindings.BindingSpec{EngineCatalog: "lake", SourceRef: "wh"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://lk:8181/catalog",
				"iceberg.rest-catalog.warehouse": "wh", "iceberg.rest-catalog.vended-credentials-enabled": "true", "fs.s3.enabled": "true",
				"iceberg.rest-catalog.security": "OAUTH2", "iceberg.rest-catalog.oauth2.credential": "id:sec",
				"iceberg.rest-catalog.oauth2.server-uri": "https://idp.example/token", "iceberg.rest-catalog.oauth2.scope": "catalog",
			},
			secrets: []string{"iceberg.rest-catalog.oauth2.credential"},
		},
		{
			name: "unity without a token",
			src:  cfg(connections.KindUnity, map[string]string{"url": "http://unity:8080"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "lake", SourceRef: "unity"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://unity:8080/api/2.1/unity-catalog/iceberg",
				"iceberg.rest-catalog.warehouse": "unity", "iceberg.rest-catalog.vended-credentials-enabled": "true", "fs.s3.enabled": "true",
			},
		},
		{
			name: "unity with a token",
			src:  cfg(connections.KindUnity, map[string]string{"url": "http://unity:8080/"}, map[string]string{"token": "tok"}),
			spec: bindings.BindingSpec{EngineCatalog: "lake", SourceRef: "unity"},
			want: map[string]string{
				"iceberg.catalog.type": "rest", "iceberg.rest-catalog.uri": "http://unity:8080/api/2.1/unity-catalog/iceberg",
				"iceberg.rest-catalog.warehouse": "unity", "iceberg.rest-catalog.vended-credentials-enabled": "true", "fs.s3.enabled": "true",
				"iceberg.rest-catalog.security": "OAUTH2", "iceberg.rest-catalog.oauth2.token": "tok",
			},
			secrets: []string{"iceberg.rest-catalog.oauth2.token"},
		},
		{
			name: "hive metastore as an iceberg catalog (default connector)",
			src: cfg(connections.KindHiveMetastore, map[string]string{
				"metastore_uri": "thrift://hms:9083", "storage.type": "s3",
				"storage.endpoint": "http://rustfs:9000", "storage.region": "us-east-1", "storage.path_style": "true",
				"storage.access_key": "rustfsadmin",
			}, map[string]string{"storage.secret_key": "rustfsadmin"}),
			spec: bindings.BindingSpec{EngineCatalog: "lake"},
			want: map[string]string{
				"iceberg.catalog.type": "hive_metastore", "hive.metastore.uri": "thrift://hms:9083",
				"fs.s3.enabled": "true", "s3.endpoint": "http://rustfs:9000", "s3.region": "us-east-1",
				"s3.path-style-access": "true", "s3.aws-access-key": "rustfsadmin", "s3.aws-secret-key": "rustfsadmin",
			},
			secrets: []string{"s3.aws-secret-key"},
		},
		{
			name: "hive metastore as a hive catalog",
			src:  cfg(connections.KindHiveMetastore, map[string]string{"metastore_uri": "thrift://hms:9083"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "warehouse", Connector: "hive"},
			want: map[string]string{"hive.metastore": "thrift", "hive.metastore.uri": "thrift://hms:9083", "fs.s3.enabled": "true"},
		},
		{
			name: "hive metastore as a delta catalog",
			src:  cfg(connections.KindHiveMetastore, map[string]string{"metastore_uri": "thrift://hms:9083"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "delta", Connector: "delta_lake"},
			want: map[string]string{"hive.metastore": "thrift", "hive.metastore.uri": "thrift://hms:9083", "fs.s3.enabled": "true"},
		},
		{
			name: "glue as an iceberg catalog (default connector)",
			src: cfg(connections.KindGlue, map[string]string{
				"region": "us-east-1", "catalog_id": "123456789012", "role_arn": "arn:aws:iam::123456789012:role/baleia",
				"access_key": "AKIA", "endpoint": "https://glue.us-east-1.amazonaws.com", "storage.region": "us-east-1",
			}, map[string]string{"secret_key": "SECRET"}),
			spec: bindings.BindingSpec{EngineCatalog: "lake"},
			want: map[string]string{
				"iceberg.catalog.type": "glue", "hive.metastore.glue.region": "us-east-1",
				"hive.metastore.glue.catalogid": "123456789012", "hive.metastore.glue.iam-role": "arn:aws:iam::123456789012:role/baleia",
				"hive.metastore.glue.aws-access-key": "AKIA", "hive.metastore.glue.aws-secret-key": "SECRET",
				"hive.metastore.glue.endpoint": "https://glue.us-east-1.amazonaws.com",
				"fs.s3.enabled":                "true", "s3.region": "us-east-1",
			},
			secrets: []string{"hive.metastore.glue.aws-secret-key"},
		},
		{
			name: "glue as a hive catalog without explicit credentials",
			src:  cfg(connections.KindGlue, map[string]string{"region": "eu-west-1"}, nil),
			spec: bindings.BindingSpec{EngineCatalog: "warehouse", Connector: "hive"},
			want: map[string]string{"hive.metastore": "glue", "hive.metastore.glue.region": "eu-west-1", "fs.s3.enabled": "true"},
		},
	}
	b := New(slog.New(slog.DiscardHandler), nil, nil)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, err := b.Project(c.spec, c.src)
			if err != nil {
				t.Fatal(err)
			}
			if !mapsEqual(p.Properties, c.want) {
				t.Errorf("properties:\n got %v\nwant %v", p.Properties, c.want)
			}
			if !slices.Equal(p.SecretKeys, c.secrets) {
				t.Errorf("secret keys = %v, want %v", p.SecretKeys, c.secrets)
			}
			if p.Version != Version(p.EngineCatalog, p.Connector, p.Properties) {
				t.Error("version is not the plugin's catalog_version")
			}
		})
	}
	for tls, want := range map[string]string{"preferred": "jdbc:mysql://my:3306", "skip-verify": "jdbc:mysql://my:3306?sslMode=REQUIRED", "false": "jdbc:mysql://my:3306?sslMode=DISABLED"} {
		p, err := b.Project(bindings.BindingSpec{EngineCatalog: "shop"}, cfg(connections.KindMySQL, map[string]string{"host": "my", "tls": tls}, nil))
		if err != nil || p.Properties["connection-url"] != want {
			t.Errorf("mysql tls=%s: %s, %v", tls, p.Properties["connection-url"], err)
		}
	}
	// Values inside ;-separated JDBC URLs cannot add parameters.
	for _, bad := range []struct {
		kind connections.Kind
		vals map[string]string
		ref  string
	}{
		{connections.KindSQLServer, map[string]string{"host": "ms"}, "sales;integratedSecurity=true"},
		{connections.KindSQLServer, map[string]string{"host": "ms"}, "{x}"},
		{connections.KindOracle, map[string]string{"host": "ora"}, "svc)(HOST=evil"},
		{connections.KindOracle, map[string]string{"host": "ora"}, ""},
	} {
		if _, err := b.Project(bindings.BindingSpec{EngineCatalog: "x", SourceRef: bad.ref}, cfg(bad.kind, bad.vals, nil)); !errors.Is(err, bindings.ErrInvalid) {
			t.Errorf("%s %q: %v", bad.kind, bad.ref, err)
		}
	}
	// A PostgreSQL host that tries to smuggle JDBC parameters is not a host;
	// the provider rejects it at validation and the URL escapes what is left.
	p, err := b.Project(bindings.BindingSpec{EngineCatalog: "x"}, cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "database": "a?socketFactory=evil"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Properties["connection-url"]; got != "jdbc:postgresql://db:5432/a%3FsocketFactory=evil" {
		t.Errorf("database name injected a parameter: %s", got)
	}
	for db, want := range map[string]string{"a/b": "jdbc:postgresql://db:5432/a%2Fb", "a/../b": "jdbc:postgresql://db:5432/a%2F..%2Fb"} {
		p, _ := b.Project(bindings.BindingSpec{EngineCatalog: "x", SourceRef: db}, cfg(connections.KindPostgreSQL, map[string]string{"host": "db"}, nil))
		if got := p.Properties["connection-url"]; got != want {
			t.Errorf("database %q: %s, want %s", db, got, want)
		}
	}
}

func TestExtraProps(t *testing.T) {
	t.Parallel()
	b := New(slog.New(slog.DiscardHandler), nil, nil)
	src := cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "pw"})
	for _, key := range []string{"connection-url", "connection-password", "connection-user", "iceberg.rest-catalog.uri", "iceberg.rest-catalog.oauth2.credential",
		"iceberg.rest-catalog.security", "s3.aws-secret-key", "s3.aws-access-key", "hive.metastore.uri", "iceberg.catalog.type", "x.token", "connector.name"} {
		_, err := b.Project(bindings.BindingSpec{EngineCatalog: "c", ExtraProps: map[string]string{key: "evil"}}, src)
		var pe *bindings.PropertyError
		if !errors.Is(err, bindings.ErrPropertyNotOverridable) || !errors.As(err, &pe) || pe.Key != key {
			t.Errorf("%s: %v, want property_not_overridable", key, err)
		}
	}
	for _, key := range []string{"", "a b", "a=b"} {
		if _, err := b.Project(bindings.BindingSpec{EngineCatalog: "c", ExtraProps: map[string]string{key: "v"}}, src); !errors.Is(err, bindings.ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", key, err)
		}
	}
	p, err := b.Project(bindings.BindingSpec{EngineCatalog: "c", ExtraProps: map[string]string{"case-insensitive-name-matching": "true"}}, src)
	if err != nil || p.Properties["case-insensitive-name-matching"] != "true" {
		t.Errorf("an ordinary property must merge: %v, %v", p.Properties, err)
	}
}

func TestReservedNamesAndConnectors(t *testing.T) {
	t.Parallel()
	b := New(slog.New(slog.DiscardHandler), nil, nil)
	src := cfg(connections.KindPostgreSQL, map[string]string{"host": "db"}, nil)
	for _, name := range b.ReservedNames() {
		if _, err := b.Project(bindings.BindingSpec{EngineCatalog: name}, src); !errors.Is(err, bindings.ErrReservedName) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"Upper", "1x", "a-b", strings.Repeat("a", 64)} {
		if _, err := b.Project(bindings.BindingSpec{EngineCatalog: name}, src); !errors.Is(err, bindings.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := b.Project(bindings.BindingSpec{EngineCatalog: "c", Connector: "iceberg"}, src); !errors.Is(err, bindings.ErrUnsupportedSource) {
		t.Errorf("postgresql through the iceberg connector: %v", err)
	}
	if _, err := b.Project(bindings.BindingSpec{EngineCatalog: "c"}, cfg(connections.KindClickHouse, map[string]string{"host": "ch"}, nil)); !errors.Is(err, bindings.ErrUnsupportedSource) {
		t.Errorf("clickhouse is not bindable yet: %v", err)
	}
}

// coordinator records the statements a fake Trino receives.
type coordinator struct {
	mu   sync.Mutex
	sqls []string
	srv  *httptest.Server
}

func newCoordinator(t *testing.T) *coordinator {
	c := &coordinator{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/statement" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.sqls = append(c.sqls, string(body))
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"20240101_000000_00000_abcde","nextUri":"","columns":[{"name":"catalog_name","type":"varchar"},{"name":"connector_name","type":"varchar"}],"data":[["lake","iceberg"],["system","system"]]}`)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// store is an in-memory registry.
type store struct {
	rows map[string]db.GetTrinoCatalogRegistryStateRow
	log  []string
}

func (s *store) UpsertTrinoCatalogRegistry(_ context.Context, arg db.UpsertTrinoCatalogRegistryParams) error {
	s.rows[arg.CatalogName] = db.GetTrinoCatalogRegistryStateRow{CatalogVersion: arg.CatalogVersion, Enabled: true, SyncStatus: "pending", UpdatedBy: "baleia"}
	s.log = append(s.log, "upsert "+arg.CatalogName)
	return nil
}

func (s *store) SoftDeleteTrinoCatalogRegistry(_ context.Context, arg db.SoftDeleteTrinoCatalogRegistryParams) error {
	r := s.rows[arg.CatalogName]
	r.Enabled = false
	s.rows[arg.CatalogName] = r
	s.log = append(s.log, "disable "+arg.CatalogName)
	return nil
}

func (s *store) GetTrinoCatalogRegistryState(_ context.Context, arg db.GetTrinoCatalogRegistryStateParams) (db.GetTrinoCatalogRegistryStateRow, error) {
	r, ok := s.rows[arg.CatalogName]
	if !ok {
		return r, pgx.ErrNoRows
	}
	return r, nil
}

// TestDDLHasNoSecrets: for every kind, the CREATE CATALOG text Trino logs
// carries placeholders, never the secret; the registry has the real value.
func TestDDLHasNoSecrets(t *testing.T) {
	t.Parallel()
	sources := []connections.Config{
		cfg(connections.KindPolaris, map[string]string{"url": "http://p:8181", "client_id": "id"}, map[string]string{"client_secret": "FIXTURE-SECRET-1"}),
		cfg(connections.KindNessie, map[string]string{"url": "http://n:19120", "warehouse": "w"}, map[string]string{"token": "FIXTURE-SECRET-2"}),
		cfg(connections.KindIcebergREST, map[string]string{"url": "http://r", "warehouse": "w", "client_id": "id"}, map[string]string{"client_secret": "FIXTURE-SECRET-3"}),
		cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "FIXTURE-SECRET-4"}),
		cfg(connections.KindMySQL, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "FIXTURE-SECRET-5"}),
		cfg(connections.KindMariaDB, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "FIXTURE-SECRET-6"}),
		cfg(connections.KindSQLServer, map[string]string{"host": "db", "database": "d", "username": "u"}, map[string]string{"password": "FIXTURE-SECRET-7"}),
		cfg(connections.KindOracle, map[string]string{"host": "db", "service_name": "S", "username": "u"}, map[string]string{"password": "FIXTURE-SECRET-8"}),
		cfg(connections.KindHiveMetastore, map[string]string{"metastore_uri": "thrift://hms:9083", "storage.access_key": "k"}, map[string]string{"storage.secret_key": "FIXTURE-SECRET-9"}),
		cfg(connections.KindLakekeeper, map[string]string{"url": "http://lk:8181", "auth_mode": "oauth2", "client_id": "id", "token_endpoint": "https://idp/token"}, map[string]string{"client_secret": "FIXTURE-SECRET-10"}),
		cfg(connections.KindUnity, map[string]string{"url": "http://unity:8080"}, map[string]string{"token": "FIXTURE-SECRET-11"}),
		cfg(connections.KindGlue, map[string]string{"region": "us-east-1", "access_key": "k"}, map[string]string{"secret_key": "FIXTURE-SECRET-12"}),
	}
	co := newCoordinator(t)
	st := &store{rows: map[string]db.GetTrinoCatalogRegistryStateRow{}}
	b := New(slog.New(slog.DiscardHandler), trinoclient.New(co.srv.URL, "baleia-admin"), st)
	eng := bindings.EngineRef{ClusterID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
	for i, src := range sources {
		p, err := b.Project(bindings.BindingSpec{EngineCatalog: "c" + string(rune('a'+i)), SourceRef: "w"}, src)
		if err != nil {
			t.Fatalf("%s: %v", src.Kind, err)
		}
		if err := b.Create(context.Background(), eng, p); err != nil {
			t.Fatalf("%s: %v", src.Kind, err)
		}
		ddl := co.sqls[len(co.sqls)-1]
		if strings.Contains(ddl, "FIXTURE-SECRET") || !strings.Contains(ddl, "@baleia-secret[") {
			t.Errorf("%s: DDL = %s", src.Kind, ddl)
		}
	}
	if !slices.Equal(st.log[:2], []string{"upsert ca", "upsert cb"}) {
		t.Errorf("the registry row is written before each CREATE: %v", st.log)
	}
}

func TestLifecycleAndConfirm(t *testing.T) {
	t.Parallel()
	co := newCoordinator(t)
	st := &store{rows: map[string]db.GetTrinoCatalogRegistryStateRow{}}
	b := New(slog.New(slog.DiscardHandler), trinoclient.New(co.srv.URL, "baleia-admin"), st)
	eng := bindings.EngineRef{ClusterID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
	ctx := context.Background()
	src := cfg(connections.KindPostgreSQL, map[string]string{"host": "db"}, nil)
	p, _ := b.Project(bindings.BindingSpec{EngineCatalog: "old"}, src)

	if c, _ := b.Confirm(ctx, eng, p); c.Confirmed || c.Reason != "not_registered" {
		t.Errorf("before create: %+v", c)
	}
	if err := b.Create(ctx, eng, p); err != nil {
		t.Fatal(err)
	}
	if c, _ := b.Confirm(ctx, eng, p); c.Confirmed || c.Reason != "plugin_not_confirmed" {
		t.Errorf("before the plugin's write: %+v", c)
	}
	r := st.rows["old"]
	r.UpdatedBy = "trino"
	st.rows["old"] = r
	if c, _ := b.Confirm(ctx, eng, p); !c.Confirmed {
		t.Errorf("after the plugin's write: %+v", c)
	}
	p2, _ := b.Project(bindings.BindingSpec{EngineCatalog: "old", ExtraProps: map[string]string{"x": "y"}}, src)
	if c, _ := b.Confirm(ctx, eng, p2); c.Confirmed || c.Reason != "version_mismatch" {
		t.Errorf("another version: %+v", c)
	}

	// Rename creates the new name before dropping the old one.
	renamed, _ := b.Project(bindings.BindingSpec{EngineCatalog: "new"}, src)
	co.sqls, st.log = nil, nil
	if err := b.Rename(ctx, eng, "old", renamed); err != nil {
		t.Fatal(err)
	}
	if len(co.sqls) != 2 || !strings.HasPrefix(co.sqls[0], "CREATE CATALOG") || !strings.Contains(co.sqls[0], `"new"`) && !strings.Contains(co.sqls[0], " new ") ||
		!strings.HasPrefix(co.sqls[1], "DROP CATALOG") {
		t.Errorf("rename statements = %q", co.sqls)
	}
	if !slices.Equal(st.log, []string{"upsert new", "disable old"}) {
		t.Errorf("rename registry = %v", st.log)
	}
	if c, _ := b.Confirm(ctx, eng, p); c.Reason != "disabled" {
		t.Errorf("old name after rename: %+v", c)
	}

	obs, err := b.Observe(ctx, eng)
	if err != nil || len(obs) != 2 || obs[0].EngineCatalog != "lake" || obs[0].Connector != "iceberg" || !obs[0].Present {
		t.Errorf("observe = %+v, %v", obs, err)
	}
}

// TestSupportsMatrixMatchesDocs lives in internal/connections/providers
// (TestConnectionsMatrixUpToDate renders the bindable column from this
// binder); this pins the binder side of it.
func TestSupports(t *testing.T) {
	t.Parallel()
	got := New(slog.New(slog.DiscardHandler), nil, nil).Supports()
	want := map[connections.Kind][]string{
		connections.KindPolaris: {"iceberg"}, connections.KindNessie: {"iceberg"}, connections.KindIcebergREST: {"iceberg"},
		connections.KindHiveMetastore: {"iceberg", "hive", "delta_lake"},
		connections.KindGlue:          {"iceberg", "hive", "delta_lake"},
		connections.KindLakekeeper:    {"iceberg"},
		connections.KindUnity:         {"iceberg"},
		connections.KindPostgreSQL:    {"postgresql"}, connections.KindMySQL: {"mysql"},
		connections.KindMariaDB: {"mariadb"}, connections.KindSQLServer: {"sqlserver"}, connections.KindOracle: {"oracle"},
	}
	if len(got) != len(want) {
		t.Fatalf("supports = %v", got)
	}
	for k, v := range want {
		if !slices.Equal(got[k], v) {
			t.Errorf("%s: %v", k, got[k])
		}
	}
}

// TestProject_SecretReference: a secret field holding an external reference is
// projected verbatim (the plugin persists the reference, not the value), needs
// no placeholder, and the version hashes the value Baleia resolved so it matches
// the plugin's catalog_version (#31).
func TestProject_SecretReference(t *testing.T) {
	t.Setenv("BALEIA_TEST_PASSWORD", "s3cret")
	b := New(slog.New(slog.DiscardHandler), nil, nil)
	ref := cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "@baleia-secret[env:BALEIA_TEST_PASSWORD]"})
	lit := cfg(connections.KindPostgreSQL, map[string]string{"host": "db", "username": "u"}, map[string]string{"password": "s3cret"})

	pRef, err := b.Project(bindings.BindingSpec{EngineCatalog: "c"}, ref)
	if err != nil {
		t.Fatal(err)
	}
	pLit, err := b.Project(bindings.BindingSpec{EngineCatalog: "c"}, lit)
	if err != nil {
		t.Fatal(err)
	}

	if pRef.Properties["connection-password"] != "@baleia-secret[env:BALEIA_TEST_PASSWORD]" {
		t.Errorf("reference not projected verbatim: %v", pRef.Properties)
	}
	if len(pRef.SecretKeys) != 0 {
		t.Errorf("a reference needs no placeholder, got secrets %v", pRef.SecretKeys)
	}
	if pRef.Version != pLit.Version {
		t.Errorf("version hashes the reference, not the resolved value: %s vs %s", pRef.Version, pLit.Version)
	}
	if pLit.Properties["connection-password"] != "s3cret" || len(pLit.SecretKeys) != 1 {
		t.Errorf("literal secret not projected: %v %v", pLit.Properties, pLit.SecretKeys)
	}
}

// TestBinder_IcebergMinRetention: the floor of #500 reaches Iceberg
// catalogs only, survives WithStore and WithRegistryKeyring, and never
// overrides an operator's extra property.
func TestBinder_IcebergMinRetention(t *testing.T) {
	t.Parallel()
	b := New(nil, nil, nil).WithIcebergMinRetention(90 * time.Minute).WithStore(nil).WithRegistryKeyring(nil)
	if b.minRetention != 90*time.Minute {
		t.Fatalf("minRetention lost through the With* options: %s", b.minRetention)
	}
	p := projection{props: map[string]string{"iceberg.remove-orphan-files.min-retention": "2d"}}
	minRetention(&p, b.minRetention)
	if p.props["iceberg.expire-snapshots.min-retention"] != "90m" || p.props["iceberg.remove-orphan-files.min-retention"] != "2d" {
		t.Fatalf("props = %v", p.props)
	}
	for d, want := range map[time.Duration]string{time.Hour: "1h", 48 * time.Hour: "2d", 90 * time.Second: "90s"} {
		if got := retentionDuration(d); got != want {
			t.Errorf("retentionDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
