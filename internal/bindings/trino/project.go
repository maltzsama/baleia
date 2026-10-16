package trino

// Projections of source connections into Trino catalogs, one function per
// source family. Property names are checked against the Trino docs of the
// version the stack runs (483):
//   - Iceberg REST catalog: https://trino.io/docs/483/object-storage/metastores.html#iceberg-rest-catalog
//   - S3 file system:       https://trino.io/docs/483/object-storage/file-system-s3.html
//   - PostgreSQL connector: https://trino.io/docs/483/connector/postgresql.html
//   - MySQL connector:      https://trino.io/docs/483/connector/mysql.html
//   - MariaDB connector:    https://trino.io/docs/483/connector/mariadb.html
//   - SQL Server connector: https://trino.io/docs/483/connector/sqlserver.html
//   - Oracle connector:     https://trino.io/docs/483/connector/oracle.html

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/lakekeeper"
	"github.com/galileostudio/baleia/internal/connections/nessie"
	"github.com/galileostudio/baleia/internal/connections/polaris"
)

// nameRe is Trino's catalog and connector name grammar, mirrored by the
// CHECK constraints on trino_catalog_registry and catalogs.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// reservedNames are Trino's built-in catalogs plus the "env" and "file"
// origins of the plugin's @baleia-secret[<origin>:<key>] syntax.
var reservedNames = []string{"system", "jmx", "tpch", "tpcds", "memory", "env", "file"}

// protectedPatterns are extra-property keys that cannot be overridden:
// endpoints, credentials and security modes come only from the connection.
var protectedPatterns = []string{
	"connection-url", "connection-user", "*.uri", "*uri", "*password*", "*credential*", "*secret*",
	"*token*", "*.security", "*access-key*", "*.catalog.type", "connector.name",
}

// projection is one source family's properties and secret keys.
type projection struct {
	props   map[string]string
	secrets []string
}

func (b *Binder) project(spec bindings.BindingSpec, src connections.Config, connector string) (projection, error) {
	switch src.Kind {
	case connections.KindPolaris:
		return polarisProjection(spec, src), nil
	case connections.KindNessie:
		return nessieProjection(spec, src)
	case connections.KindIcebergREST:
		return icebergRESTProjection(spec, src)
	case connections.KindHiveMetastore:
		return hiveProjection(connector, src)
	case connections.KindLakekeeper:
		return lakekeeperProjection(spec, src)
	case connections.KindPostgreSQL:
		return postgresProjection(spec, src)
	case connections.KindMySQL:
		return mysqlProjection(src)
	case connections.KindMariaDB:
		return mariadbProjection(src)
	case connections.KindSQLServer:
		return sqlserverProjection(spec, src)
	case connections.KindOracle:
		return oracleProjection(spec, src)
	}
	return projection{}, fmt.Errorf("%w: %s", bindings.ErrUnsupportedSource, src.Kind)
}

// polarisProjection is byte-identical to the projection before #80 (golden
// version d0e1bd09…): changing it would force every Polaris catalog to
// resync.
func polarisProjection(spec bindings.BindingSpec, src connections.Config) projection {
	return projection{
		props: map[string]string{
			"iceberg.catalog.type":                            "rest",
			"iceberg.rest-catalog.uri":                        polaris.New(nil).RESTURI(src),
			"iceberg.rest-catalog.warehouse":                  spec.SourceRef,
			"iceberg.rest-catalog.security":                   "OAUTH2",
			"iceberg.rest-catalog.oauth2.credential":          src.Value("client_id") + ":" + src.Secret("client_secret"),
			"iceberg.rest-catalog.oauth2.scope":               "PRINCIPAL_ROLE:ALL",
			"iceberg.rest-catalog.vended-credentials-enabled": "true",
			"fs.s3.enabled":                                   "true",
		},
		secrets: []string{"iceberg.rest-catalog.oauth2.credential"},
	}
}

// icebergREST is the shared shape of REST catalogs that are not Polaris.
func icebergREST(uri, warehouse string, src connections.Config) projection {
	p := projection{props: map[string]string{
		"iceberg.catalog.type":           "rest",
		"iceberg.rest-catalog.uri":       uri,
		"iceberg.rest-catalog.warehouse": warehouse,
		"fs.s3.enabled":                  "true",
	}}
	switch {
	case src.Secret("client_secret") != "":
		p.props["iceberg.rest-catalog.security"] = "OAUTH2"
		p.props["iceberg.rest-catalog.oauth2.credential"] = src.Value("client_id") + ":" + src.Secret("client_secret")
		if s := src.Value("scope"); s != "" {
			p.props["iceberg.rest-catalog.oauth2.scope"] = s
		}
		if s := src.Value("oauth2_server_uri"); s != "" {
			p.props["iceberg.rest-catalog.oauth2.server-uri"] = s
		}
		p.secrets = []string{"iceberg.rest-catalog.oauth2.credential"}
	case src.Secret("token") != "":
		p.props["iceberg.rest-catalog.security"] = "OAUTH2"
		p.props["iceberg.rest-catalog.oauth2.token"] = src.Secret("token")
		p.secrets = []string{"iceberg.rest-catalog.oauth2.token"}
	default:
		p.props["iceberg.rest-catalog.security"] = "NONE"
	}
	return p
}

func warehouseOf(spec bindings.BindingSpec, src connections.Config) (string, error) {
	w := spec.SourceRef
	if w == "" {
		w = src.Value("warehouse")
	}
	if w == "" {
		return "", fmt.Errorf("%w: warehouse is required", bindings.ErrInvalid)
	}
	return w, nil
}

func nessieProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	w, err := warehouseOf(spec, src)
	if err != nil {
		return projection{}, err
	}
	return icebergREST(nessie.New(nil).RESTURI(src), w, src), nil
}

func icebergRESTProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	w, err := warehouseOf(spec, src)
	if err != nil {
		return projection{}, err
	}
	return icebergREST(strings.TrimRight(src.Value("url"), "/"), w, src), nil
}

// lakekeeperProjection binds a Lakekeeper warehouse as an Iceberg REST catalog.
// The warehouse (the binding's source ref) is the addressing key; OAuth2 client
// credentials authenticate when auth_mode=oauth2. The OAuth2 credential is the
// only secret; the rest are real connection values. Property names for Trino
// 483: https://trino.io/docs/483/connector/iceberg.html
func lakekeeperProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	w, err := warehouseOf(spec, src)
	if err != nil {
		return projection{}, err
	}
	p := projection{props: map[string]string{
		"iceberg.catalog.type":                            "rest",
		"iceberg.rest-catalog.uri":                        lakekeeper.New(nil).RESTURI(src),
		"iceberg.rest-catalog.warehouse":                  w,
		"iceberg.rest-catalog.vended-credentials-enabled": "true",
		"fs.s3.enabled":                                   "true",
	}}
	if src.Value("auth_mode") == "oauth2" {
		p.props["iceberg.rest-catalog.security"] = "OAUTH2"
		p.props["iceberg.rest-catalog.oauth2.credential"] = src.Value("client_id") + ":" + src.Secret("client_secret")
		if s := src.Value("token_endpoint"); s != "" {
			p.props["iceberg.rest-catalog.oauth2.server-uri"] = s
		}
		if s := src.Value("scope"); s != "" {
			p.props["iceberg.rest-catalog.oauth2.scope"] = s
		}
		p.secrets = append(p.secrets, "iceberg.rest-catalog.oauth2.credential")
	}
	return p, nil
}

// hiveProjection binds a Hive Metastore connection as an Iceberg, Hive or
// Delta Lake catalog. One connection can hold tables of every format; the
// binding's connector picks which one the catalog exposes. Trino reads the
// metastore over Thrift (hive.metastore.uri) and the table data from
// S3-compatible storage. Property names for Trino 483:
//   - Iceberg:    https://trino.io/docs/483/connector/iceberg.html
//   - Hive:       https://trino.io/docs/483/connector/hive.html
//   - Delta Lake: https://trino.io/docs/483/connector/delta-lake.html
func hiveProjection(connector string, src connections.Config) (projection, error) {
	uri := src.Value("metastore_uri")
	if uri == "" {
		return projection{}, fmt.Errorf("%w: the connection has no metastore URI", bindings.ErrInvalid)
	}
	p := projection{props: map[string]string{"hive.metastore.uri": uri}}
	switch connector {
	case "iceberg":
		p.props["iceberg.catalog.type"] = "hive_metastore"
	case "hive":
		p.props["hive.metastore"] = "thrift"
	case "delta_lake":
		p.props["hive.metastore"] = "thrift"
	}
	s3Storage(&p, src)
	return p, nil
}

// s3Storage adds the S3 file-system properties of an object-storage
// connection the metastore's table data lives on. The secret key never
// appears in DDL text or logs.
func s3Storage(p *projection, src connections.Config) {
	p.props["fs.s3.enabled"] = "true"
	if e := src.Value("storage.endpoint"); e != "" {
		p.props["s3.endpoint"] = e
	}
	if r := src.Value("storage.region"); r != "" {
		p.props["s3.region"] = r
	}
	if src.Value("storage.path_style") == "true" {
		p.props["s3.path-style-access"] = "true"
	}
	if k := src.Value("storage.access_key"); k != "" {
		p.props["s3.aws-access-key"] = k
	}
	if s := src.Secret("storage.secret_key"); s != "" {
		p.props["s3.aws-secret-key"] = s
		p.secrets = append(p.secrets, "s3.aws-secret-key")
	}
}

// hostPort is the validated host and port of a database connection.
func hostPort(src connections.Config, defaultPort string) (string, error) {
	host, port := src.Value("host"), src.Value("port")
	if host == "" {
		return "", fmt.Errorf("%w: the connection has no host", bindings.ErrInvalid)
	}
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(host, port), nil
}

// sqlCredentials adds connection-user and connection-password.
func sqlCredentials(p *projection, src connections.Config) {
	if u := src.Value("username"); u != "" {
		p.props["connection-user"] = u
	}
	if pw := src.Secret("password"); pw != "" {
		p.props["connection-password"] = pw
		p.secrets = append(p.secrets, "connection-password")
	}
}

// postgresProjection builds the JDBC URL from validated fields; users never
// supply one (a raw URL could carry socketFactory and similar parameters).
// The database is the binding's source ref, else the connection's.
func postgresProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	addr, err := hostPort(src, "5432")
	if err != nil {
		return projection{}, err
	}
	database := spec.SourceRef
	if database == "" {
		database = src.Value("database")
	}
	if database == "" {
		database = "postgres"
	}
	// One path segment, escaped: pgJDBC decodes a%2Fb back to a/b, and ?, #
	// or / in a name can never become parameters or extra segments.
	u := url.URL{Scheme: "postgresql", Host: addr, Path: "/" + database, RawPath: "/" + url.PathEscape(database)}
	q := url.Values{}
	if mode := src.Value("sslmode"); mode != "" {
		q.Set("sslmode", mode)
	}
	u.RawQuery = q.Encode()
	p := projection{props: map[string]string{"connection-url": "jdbc:" + u.String()}}
	sqlCredentials(&p, src)
	return p, nil
}

// mysqlProjection connects to the server; Trino's MySQL catalogs expose every
// database as a schema, so the URL has no database.
func mysqlProjection(src connections.Config) (projection, error) {
	addr, err := hostPort(src, "3306")
	if err != nil {
		return projection{}, err
	}
	jdbc := "jdbc:mysql://" + addr
	// The provider's tls modes as Connector/J sslMode; preferred is the
	// driver's default.
	if mode, ok := map[string]string{"true": "VERIFY_IDENTITY", "skip-verify": "REQUIRED", "false": "DISABLED"}[src.Value("tls")]; ok {
		jdbc += "?sslMode=" + mode
	}
	p := projection{props: map[string]string{"connection-url": jdbc}}
	sqlCredentials(&p, src)
	return p, nil
}

// mariadbProjection is mysqlProjection for MariaDB Connector/J.
func mariadbProjection(src connections.Config) (projection, error) {
	addr, err := hostPort(src, "3306")
	if err != nil {
		return projection{}, err
	}
	jdbc := "jdbc:mariadb://" + addr
	if mode, ok := map[string]string{"true": "verify-full", "skip-verify": "trust", "false": "disable"}[src.Value("tls")]; ok {
		jdbc += "?sslMode=" + mode
	}
	p := projection{props: map[string]string{"connection-url": jdbc}}
	sqlCredentials(&p, src)
	return p, nil
}

// jdbcValueRe is what a value inside a ;-separated JDBC URL may hold: a
// database name with ; = { or } would add or end parameters.
var jdbcValueRe = regexp.MustCompile(`^[A-Za-z0-9_$.#@ -]{1,128}$`)

// sqlserverProjection: one catalog per database; the database is the
// binding's source ref, else the connection's.
func sqlserverProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	addr, err := hostPort(src, "1433")
	if err != nil {
		return projection{}, err
	}
	database := spec.SourceRef
	if database == "" {
		database = src.Value("database")
	}
	jdbc := "jdbc:sqlserver://" + addr
	if database != "" {
		if !jdbcValueRe.MatchString(database) {
			return projection{}, fmt.Errorf("%w: database name %q", bindings.ErrInvalid, database)
		}
		jdbc += ";databaseName=" + database
	}
	// The provider's encrypt modes; disable turns TLS off entirely.
	encrypt := map[string]string{"true": "true", "false": "false", "disable": "false", "strict": "strict"}[src.Value("encrypt")]
	if encrypt == "" {
		encrypt = "true"
	}
	jdbc += ";encrypt=" + encrypt
	if src.Value("trust_server_certificate") == "true" {
		jdbc += ";trustServerCertificate=true"
	}
	p := projection{props: map[string]string{"connection-url": jdbc}}
	sqlCredentials(&p, src)
	return p, nil
}

// serviceRe is an Oracle service name.
var serviceRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#.-]{0,127}$`)

// oracleProjection connects to a service; the binding's source ref, else the
// connection's service name.
func oracleProjection(spec bindings.BindingSpec, src connections.Config) (projection, error) {
	addr, err := hostPort(src, "1521")
	if err != nil {
		return projection{}, err
	}
	service := spec.SourceRef
	if service == "" {
		service = src.Value("service_name")
	}
	if !serviceRe.MatchString(service) {
		return projection{}, fmt.Errorf("%w: service name %q", bindings.ErrInvalid, service)
	}
	p := projection{props: map[string]string{"connection-url": "jdbc:oracle:thin:@//" + addr + "/" + service}}
	sqlCredentials(&p, src)
	return p, nil
}

// protected reports whether an extra property may not override a default.
func protected(key string) bool {
	k := strings.ToLower(key)
	for _, pat := range protectedPatterns {
		if ok, _ := path.Match(pat, k); ok {
			return true
		}
	}
	return false
}

// mergeExtra applies a binding's extra properties over the projection.
func mergeExtra(p *projection, extra map[string]string) error {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, " \t\r\n=") {
			return &bindings.PropertyError{Key: k, Err: fmt.Errorf("%w: not a property name", bindings.ErrInvalid)}
		}
		if protected(k) {
			return &bindings.PropertyError{Key: k, Err: bindings.ErrPropertyNotOverridable}
		}
		p.props[k] = extra[k]
	}
	return nil
}
