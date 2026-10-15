// Package sqlserver is the SQL Server provider. Metadata is read from the
// catalog views with microsoft/go-mssqldb; every connection is dialed through
// connections.Dialer (Connector.Dialer: the driver resolves the name, the
// dialer checks and dials each resolved address).
package sqlserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	mssql "github.com/microsoft/go-mssqldb"
)

const (
	statementTimeout = 30 * time.Second
	connectTimeout   = 10 * time.Second
	pageSize         = 1000
)

var encryptModes = []string{"true", "false", "disable", "strict"}

// systemDatabases are hidden from metadata listings.
var systemDatabases = []string{"master", "tempdb", "model", "msdb"}

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindSQLServer }
func (*Provider) Label() string              { return "SQL Server" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "sqlserver" }

// WalksCatalogs: the reader reads a whole catalog in one query (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "Port", Type: connections.FieldInt, Default: "1433"},
		{Key: "database", Label: "Database", Type: connections.FieldText, Default: "master", Help: "Database to connect to; every database the login may access is listed"},
		{Key: "encrypt", Label: "Encrypt", Type: connections.FieldSelect, Default: "true", Options: encryptModes},
		{Key: "trust_server_certificate", Label: "Trust server certificate", Type: connections.FieldBool, Default: "false"},
		{Key: "username", Label: "Username", Type: connections.FieldText, Required: true},
		{Key: "password", Label: "Password", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	if err := connections.Port(out.Values["port"]); err != nil {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "port", Message: err.Error()}}}
	}
	if strings.ContainsAny(out.Values["host"], "/ ,;\\") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "host", Message: "must be a single host name or address"}}}
	}
	return out, nil
}

// connector builds a URL-form DSN (values escaped by net/url) and hooks the
// dialer into the connector.
func (p *Provider) connector(cfg connections.Config) (*mssql.Connector, error) {
	port := cfg.Value("port")
	if port == "" {
		port = "1433"
	}
	q := url.Values{}
	q.Set("database", valueOr(cfg.Value("database"), "master"))
	q.Set("encrypt", valueOr(cfg.Value("encrypt"), "true"))
	q.Set("TrustServerCertificate", valueOr(cfg.Value("trust_server_certificate"), "false"))
	q.Set("app name", "baleia")
	q.Set("dial timeout", fmt.Sprint(int(connectTimeout.Seconds())))
	u := &url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.Value("username"), cfg.Secret("password")),
		Host:     net.JoinHostPort(cfg.Value("host"), port),
		RawQuery: q.Encode(),
	}
	c, err := mssql.NewConnector(u.String())
	if err != nil {
		return nil, fmt.Errorf("sqlserver config: %w", connections.ErrValidation)
	}
	c.Dialer = p.dialer
	return c, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (p *Provider) open(cfg connections.Config) (*sql.DB, error) {
	c, err := p.connector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	db, err := p.open(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	db, err := p.open(cfg)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &reader{db: db}, nil
}

// reader queries the catalog views of each database through three-part
// names, so one connection serves every database the login can access.
type reader struct {
	db *sql.DB
}

// quoteIdent brackets a name, doubling closing brackets.
func quoteIdent(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }

func (r *reader) ListCatalogs(ctx context.Context) ([]connections.CatalogRef, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT name FROM sys.databases
		WHERE HAS_DBACCESS(name) = 1 AND state = 0 AND name NOT IN (@p1, @p2, @p3, @p4)
		ORDER BY name`, systemDatabases[0], systemDatabases[1], systemDatabases[2], systemDatabases[3])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []connections.CatalogRef
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, connections.CatalogRef{Name: name, Kind: "database"})
	}
	return out, rows.Err()
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.name FROM %[1]s.sys.schemas s
		JOIN %[1]s.sys.database_principals p ON p.principal_id = s.principal_id
		WHERE s.name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest')
		  AND s.name NOT LIKE 'db[_]%%'
		ORDER BY s.name`, quoteIdent(catalog)))
	if err != nil {
		return nil, notFoundOr(err)
	}
	defer rows.Close()
	out := []connections.Namespace{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, connections.Namespace{name})
	}
	return out, rows.Err()
}

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p3) TABLE_NAME, TABLE_TYPE FROM %s.INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME > @p2
		ORDER BY TABLE_NAME`, quoteIdent(catalog)), ns[0], pageToken, pageSize+1)
	if err != nil {
		return nil, "", notFoundOr(err)
	}
	defer rows.Close()
	out := []connections.TableRef{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, "", err
		}
		kind := "TABLE"
		if typ == "VIEW" {
			kind = "VIEW"
		}
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: ns, Name: name, Type: kind})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = out[pageSize-1].Name
	}
	return out, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	db := quoteIdent(t.Catalog)
	var typ string
	err := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT TABLE_TYPE FROM %s.INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2`, db),
		t.Namespace[0], t.Name).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	if err != nil {
		return nil, notFoundOr(err)
	}
	// Column comments are MS_Description extended properties.
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT c.COLUMN_NAME,
		       c.DATA_TYPE + CASE
		           WHEN c.CHARACTER_MAXIMUM_LENGTH = -1 THEN '(max)'
		           WHEN c.CHARACTER_MAXIMUM_LENGTH IS NOT NULL THEN '(' + CAST(c.CHARACTER_MAXIMUM_LENGTH AS varchar(10)) + ')'
		           WHEN c.DATA_TYPE IN ('decimal', 'numeric') THEN '(' + CAST(c.NUMERIC_PRECISION AS varchar(3)) + ',' + CAST(c.NUMERIC_SCALE AS varchar(3)) + ')'
		           ELSE '' END,
		       CASE WHEN c.IS_NULLABLE = 'YES' THEN 1 ELSE 0 END,
		       COALESCE((
		           SELECT CAST(ep.value AS nvarchar(4000))
		           FROM %[1]s.sys.columns sc
		           JOIN %[1]s.sys.extended_properties ep
		                ON ep.major_id = sc.object_id AND ep.minor_id = sc.column_id AND ep.class = 1
		           WHERE ep.name = 'MS_Description' AND sc.name = c.COLUMN_NAME
		             AND sc.object_id = OBJECT_ID(QUOTENAME(@p3) + '.' + QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME))
		       ), ''),
		       c.ORDINAL_POSITION
		FROM %[1]s.INFORMATION_SCHEMA.COLUMNS c
		WHERE c.TABLE_SCHEMA = @p1 AND c.TABLE_NAME = @p2
		ORDER BY c.ORDINAL_POSITION`, db), t.Namespace[0], t.Name, t.Catalog)
	if err != nil {
		return nil, notFoundOr(err)
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		var nullable int
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &c.Comment, &c.Position); err != nil {
			return nil, err
		}
		c.Nullable = nullable == 1
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ref := t
	ref.Type = "TABLE"
	if typ == "VIEW" {
		ref.Type = "VIEW"
	}
	return &connections.TableMeta{Ref: ref, Columns: cols, Format: "sqlserver", Properties: map[string]string{}}, nil
}

// notFoundOr maps "database does not exist / no access" to ErrNotFound.
func notFoundOr(err error) error {
	var me mssql.Error
	if errors.As(err, &me) && (me.Number == 911 || me.Number == 916 || me.Number == 208) {
		return fmt.Errorf("%w: %s", connections.ErrNotFound, me.Message)
	}
	return err
}

// WalkCatalog reads every table and view of a database with its columns in
// one query.
func (r *reader) WalkCatalog(ctx context.Context, catalog string, visit func(connections.TableMeta) error) error {
	ctx, cancel := context.WithTimeout(ctx, connections.WalkTimeout)
	defer cancel()
	// Column comments are MS_Description extended properties.
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.TABLE_SCHEMA, t.TABLE_NAME, t.TABLE_TYPE,
		       COALESCE(c.COLUMN_NAME, ''),
		       COALESCE(c.DATA_TYPE + CASE
		           WHEN c.CHARACTER_MAXIMUM_LENGTH = -1 THEN '(max)'
		           WHEN c.CHARACTER_MAXIMUM_LENGTH IS NOT NULL THEN '(' + CAST(c.CHARACTER_MAXIMUM_LENGTH AS varchar(10)) + ')'
		           WHEN c.DATA_TYPE IN ('decimal', 'numeric') THEN '(' + CAST(c.NUMERIC_PRECISION AS varchar(3)) + ',' + CAST(c.NUMERIC_SCALE AS varchar(3)) + ')'
		           ELSE '' END, ''),
		       CASE WHEN c.IS_NULLABLE = 'NO' THEN 0 ELSE 1 END,
		       COALESCE(CAST(ep.value AS nvarchar(4000)), ''),
		       COALESCE(c.ORDINAL_POSITION, 0)
		FROM %[1]s.INFORMATION_SCHEMA.TABLES t
		LEFT JOIN %[1]s.INFORMATION_SCHEMA.COLUMNS c
		       ON c.TABLE_SCHEMA = t.TABLE_SCHEMA AND c.TABLE_NAME = t.TABLE_NAME
		LEFT JOIN %[1]s.sys.columns sc
		       ON sc.object_id = OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(t.TABLE_SCHEMA) + '.' + QUOTENAME(t.TABLE_NAME))
		      AND sc.name = c.COLUMN_NAME
		LEFT JOIN %[1]s.sys.extended_properties ep
		       ON ep.class = 1 AND ep.name = 'MS_Description' AND ep.major_id = sc.object_id AND ep.minor_id = sc.column_id
		WHERE t.TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest') AND t.TABLE_SCHEMA NOT LIKE 'db[_]%%'
		ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME, c.ORDINAL_POSITION`, quoteIdent(catalog)), catalog)
	if err != nil {
		return notFoundOr(err)
	}
	defer rows.Close()
	g := connections.NewTableGrouper(catalog, visit)
	for rows.Next() {
		var schema, typ string
		var nullable int
		var row connections.CatalogRow
		if err := rows.Scan(&schema, &row.Table, &typ, &row.Column.Name, &row.Column.NativeType,
			&nullable, &row.Column.Comment, &row.Column.Position); err != nil {
			return err
		}
		row.Namespace = connections.Namespace{schema}
		row.Column.Nullable = nullable == 1
		row.Type, row.Format = "TABLE", "sqlserver"
		if typ == "VIEW" {
			row.Type = "VIEW"
		}
		if err := g.Add(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return g.Flush()
}

func (r *reader) Close() error { return r.db.Close() }
