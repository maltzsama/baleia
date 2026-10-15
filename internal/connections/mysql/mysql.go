// Package mysql is the MySQL provider. Metadata is read from
// information_schema with go-sql-driver/mysql in read-only sessions with a
// statement timeout; connections dial through connections.Dialer, registered
// as a named network (mysql.RegisterDialContext).
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	driver "github.com/go-sql-driver/mysql"
)

const (
	statementTimeout = 30 * time.Second
	connectTimeout   = 10 * time.Second
	pageSize         = 1000
	// Catalog is the only catalog of a MySQL server: information_schema
	// reports every schema under CATALOG_NAME 'def'.
	Catalog = "def"
)

var tlsModes = []string{"preferred", "true", "skip-verify", "false"}

// systemSchemas are hidden from metadata listings.
var systemSchemas = []string{"mysql", "information_schema", "performance_schema", "sys"}

type Provider struct {
	network string
	mariadb bool
}

// New is the MySQL provider. It registers the dialer as a named network for
// the driver; the name is unique per dialer, so tests with different
// dialers do not collide.
func New(d *connections.Dialer) *Provider {
	return &Provider{network: registerDialer(d)}
}

// NewMariaDB is the MariaDB provider: same protocol and metadata, MariaDB's
// session variables.
func NewMariaDB(d *connections.Dialer) *Provider {
	return &Provider{network: registerDialer(d), mariadb: true}
}

func registerDialer(d *connections.Dialer) string {
	network := fmt.Sprintf("baleia-%p", d)
	driver.RegisterDialContext(network, func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	})
	return network
}

func (p *Provider) Kind() connections.Kind {
	if p.mariadb {
		return connections.KindMariaDB
	}
	return connections.KindMySQL
}

func (p *Provider) Label() string {
	if p.mariadb {
		return "MariaDB"
	}
	return "MySQL"
}

func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (p *Provider) EngineKind() string { return string(p.Kind()) }

// WalksCatalogs: the reader reads a whole catalog in one query (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "Port", Type: connections.FieldInt, Default: "3306"},
		{Key: "database", Label: "Default database", Type: connections.FieldText},
		{Key: "tls", Label: "TLS", Type: connections.FieldSelect, Default: "preferred", Options: tlsModes},
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
	if strings.ContainsAny(out.Values["host"], "/ ,()@") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "host", Message: "must be a single host name or address"}}}
	}
	return out, nil
}

func (p *Provider) open(cfg connections.Config) (*sql.DB, error) {
	port := cfg.Value("port")
	if port == "" {
		port = "3306"
	}
	tls := cfg.Value("tls")
	if tls == "" {
		tls = "preferred"
	}
	mc := driver.NewConfig()
	mc.Net = p.network
	mc.Addr = net.JoinHostPort(cfg.Value("host"), port)
	mc.User = cfg.Value("username")
	mc.Passwd = cfg.Secret("password")
	mc.DBName = cfg.Value("database")
	mc.TLSConfig = tls
	mc.Timeout = connectTimeout
	mc.ReadTimeout = statementTimeout
	mc.WriteTimeout = statementTimeout
	if p.mariadb {
		mc.Params = map[string]string{
			"tx_read_only":       "1",
			"max_statement_time": fmt.Sprint(int(statementTimeout.Seconds())),
		}
	} else {
		mc.Params = map[string]string{
			"transaction_read_only": "1",
			"max_execution_time":    fmt.Sprint(statementTimeout.Milliseconds()),
		}
	}
	connector, err := driver.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("mysql config: %w", connections.ErrValidation)
	}
	db := sql.OpenDB(connector)
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

type reader struct {
	db *sql.DB
}

func (r *reader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: Catalog, Kind: "server"}}, nil
}

func checkCatalog(catalog string) error {
	if catalog != Catalog {
		return fmt.Errorf("%w: catalog", connections.ErrNotFound)
	}
	return nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
		WHERE SCHEMA_NAME NOT IN (?, ?, ?, ?)
		ORDER BY SCHEMA_NAME`, systemSchemas[0], systemSchemas[1], systemSchemas[2], systemSchemas[3])
	if err != nil {
		return nil, err
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

var tableTypes = map[string]string{"BASE TABLE": "TABLE", "VIEW": "VIEW", "SYSTEM VIEW": "VIEW"}

// ListTables pages by name: the token is the last name of the previous page.
func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, "", err
	}
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME > ?
		ORDER BY TABLE_NAME
		LIMIT ?`, ns[0], pageToken, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []connections.TableRef
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, "", err
		}
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: ns, Name: name, Type: tableTypes[typ]})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = out[pageSize-1].Name
	}
	if out == nil {
		out = []connections.TableRef{}
	}
	return out, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	var typ, comment string
	var engine sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT TABLE_TYPE, ENGINE, TABLE_COMMENT FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, t.Namespace[0], t.Name).Scan(&typ, &engine, &comment)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_COMMENT, ORDINAL_POSITION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, t.Namespace[0], t.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		if err := rows.Scan(&c.Name, &c.NativeType, &c.Nullable, &c.Comment, &c.Position); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ref := t
	ref.Type = tableTypes[typ]
	format := strings.ToLower(engine.String)
	return &connections.TableMeta{
		Ref:        ref,
		Columns:    cols,
		Comment:    comment,
		Format:     format,
		Properties: map[string]string{"engine": engine.String},
	}, nil
}

// WalkCatalog reads every table and view of the server with its columns in
// one query.
func (r *reader) WalkCatalog(ctx context.Context, catalog string, visit func(connections.TableMeta) error) error {
	if err := checkCatalog(catalog); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, connections.WalkTimeout)
	defer cancel()
	// The hint lifts the session's max_execution_time for this read (MySQL;
	// MariaDB reads it as a comment).
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT /*+ MAX_EXECUTION_TIME(%d) */ t.TABLE_SCHEMA, t.TABLE_NAME, t.TABLE_TYPE, COALESCE(t.ENGINE, ''), COALESCE(t.TABLE_COMMENT, ''),
		       COALESCE(c.COLUMN_NAME, ''), COALESCE(c.COLUMN_TYPE, ''), COALESCE(c.IS_NULLABLE = 'YES', 1),
		       COALESCE(c.COLUMN_COMMENT, ''), COALESCE(c.ORDINAL_POSITION, 0)
		FROM information_schema.TABLES t
		LEFT JOIN information_schema.COLUMNS c ON c.TABLE_SCHEMA = t.TABLE_SCHEMA AND c.TABLE_NAME = t.TABLE_NAME
		WHERE t.TABLE_SCHEMA NOT IN (?, ?, ?, ?)
		ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME, c.ORDINAL_POSITION`, connections.WalkTimeout.Milliseconds()),
		systemSchemas[0], systemSchemas[1], systemSchemas[2], systemSchemas[3])
	if err != nil {
		return err
	}
	defer rows.Close()
	g := connections.NewTableGrouper(catalog, visit)
	for rows.Next() {
		var schema, typ, engine string
		var row connections.CatalogRow
		if err := rows.Scan(&schema, &row.Table, &typ, &engine, &row.Comment,
			&row.Column.Name, &row.Column.NativeType, &row.Column.Nullable, &row.Column.Comment, &row.Column.Position); err != nil {
			return err
		}
		row.Namespace = connections.Namespace{schema}
		row.Type, row.Format = tableTypes[typ], strings.ToLower(engine)
		row.Properties = map[string]string{"engine": engine}
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
