// Package postgresql is the PostgreSQL provider. Metadata is read with pgx from
// the system catalogs in read-only sessions with a statement timeout; every
// connection is dialed through connections.Dialer (pgconn DialFunc).
package postgresql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/jackc/pgx/v5"
)

const (
	statementTimeout = 30 * time.Second
	connectTimeout   = 10 * time.Second
	pageSize         = 1000
)

var sslModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindPostgreSQL }
func (*Provider) Label() string              { return "PostgreSQL" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "postgresql" }

// WalksCatalogs: the reader reads a whole catalog in one query (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "Port", Type: connections.FieldInt, Default: "5432"},
		{Key: "database", Label: "Database", Type: connections.FieldText, Default: "postgres", Help: "Database to connect to; every database the user may connect to is listed"},
		{Key: "sslmode", Label: "SSL mode", Type: connections.FieldSelect, Default: "prefer", Options: sslModes},
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
	if strings.ContainsAny(out.Values["host"], "/ ,") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "host", Message: "must be a single host name or address"}}}
	}
	return out, nil
}

// connConfig builds a pgx config from quoted keyword/value pairs, so no value
// can inject extra connection parameters. The password is set afterwards and
// never enters the string.
func (p *Provider) connConfig(cfg connections.Config, database string) (*pgx.ConnConfig, error) {
	port := cfg.Value("port")
	if port == "" {
		port = "5432"
	}
	dsn := strings.Join([]string{
		"host=" + quote(cfg.Value("host")),
		"port=" + quote(port),
		"dbname=" + quote(database),
		"user=" + quote(cfg.Value("username")),
		"sslmode=" + quote(sslMode(cfg)),
	}, " ")
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgresql config: %w", connections.ErrValidation)
	}
	cc.Password = cfg.Secret("password")
	cc.ConnectTimeout = connectTimeout
	cc.DialFunc = p.dialer.DialContext
	// Resolution happens in the dialer, on the address it dials.
	cc.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cc.RuntimeParams["default_transaction_read_only"] = "on"
	cc.RuntimeParams["statement_timeout"] = fmt.Sprint(statementTimeout.Milliseconds())
	cc.RuntimeParams["application_name"] = "baleia"
	return cc, nil
}

func quote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

func sslMode(cfg connections.Config) string {
	if m := cfg.Value("sslmode"); m != "" {
		return m
	}
	return "prefer"
}

func defaultDatabase(cfg connections.Config) string {
	if d := cfg.Value("database"); d != "" {
		return d
	}
	return "postgres"
}

func (p *Provider) connect(ctx context.Context, cfg connections.Config, database string) (*pgx.Conn, error) {
	cc, err := p.connConfig(cfg, database)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cc)
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	conn, err := p.connect(ctx, cfg, defaultDatabase(cfg))
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	return conn.Ping(ctx)
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	r := &reader{p: p, cfg: cfg, conns: map[string]*pgx.Conn{}}
	if _, err := r.conn(ctx, defaultDatabase(cfg)); err != nil {
		return nil, err
	}
	return r, nil
}

// reader keeps one connection per database: PostgreSQL only exposes the
// schemas of the database a session is connected to.
type reader struct {
	p     *Provider
	cfg   connections.Config
	mu    sync.Mutex
	conns map[string]*pgx.Conn
}

func (r *reader) conn(ctx context.Context, database string) (*pgx.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.conns[database]; ok {
		return c, nil
	}
	c, err := r.p.connect(ctx, r.cfg, database)
	if err != nil {
		return nil, err
	}
	r.conns[database] = c
	return c, nil
}

func (r *reader) ListCatalogs(ctx context.Context) ([]connections.CatalogRef, error) {
	c, err := r.conn(ctx, defaultDatabase(r.cfg))
	if err != nil {
		return nil, err
	}
	rows, err := c.Query(ctx, `
		SELECT datname FROM pg_catalog.pg_database
		WHERE datallowconn AND NOT datistemplate
		  AND has_database_privilege(datname, 'CONNECT')
		ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]connections.CatalogRef, 0, len(names))
	for _, n := range names {
		out = append(out, connections.CatalogRef{Name: n, Kind: "database"})
	}
	return out, nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if len(parent) > 0 {
		return []connections.Namespace{}, nil // schemas do not nest
	}
	c, err := r.conn(ctx, catalog)
	if err != nil {
		return nil, err
	}
	rows, err := c.Query(ctx, `
		SELECT nspname FROM pg_catalog.pg_namespace
		WHERE nspname NOT IN ('pg_catalog', 'information_schema')
		  AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp\_%'
		  AND has_schema_privilege(oid, 'USAGE')
		ORDER BY nspname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]connections.Namespace, 0, len(names))
	for _, n := range names {
		out = append(out, connections.Namespace{n})
	}
	return out, nil
}

var relTypes = map[string]string{"r": "TABLE", "p": "TABLE", "f": "TABLE", "v": "VIEW", "m": "MATERIALIZED_VIEW"}

// ListTables pages by name: the token is the last name of the previous page.
func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	c, err := r.conn(ctx, catalog)
	if err != nil {
		return nil, "", err
	}
	rows, err := c.Query(ctx, `
		SELECT c.relname, c.relkind::text FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'f', 'v', 'm')
		  AND c.relname > $2 AND NOT c.relispartition
		ORDER BY c.relname
		LIMIT $3`, ns[0], pageToken, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	type rel struct {
		Name string
		Kind string
	}
	rels, err := pgx.CollectRows(rows, pgx.RowToStructByPos[rel])
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rels) > pageSize {
		rels = rels[:pageSize]
		next = rels[pageSize-1].Name
	}
	out := make([]connections.TableRef, 0, len(rels))
	for _, t := range rels {
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: ns, Name: t.Name, Type: relTypes[t.Kind]})
	}
	return out, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	c, err := r.conn(ctx, t.Catalog)
	if err != nil {
		return nil, err
	}
	var (
		oid     uint32
		kind    string
		comment string
	)
	err = c.QueryRow(ctx, `
		SELECT c.oid, c.relkind::text, coalesce(obj_description(c.oid, 'pg_class'), '')
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'f', 'v', 'm')`,
		t.Namespace[0], t.Name).Scan(&oid, &kind, &comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	rows, err := c.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
		       coalesce(col_description(a.attrelid, a.attnum), ''), a.attnum::int
		FROM pg_catalog.pg_attribute a
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, oid)
	if err != nil {
		return nil, err
	}
	cols, err := pgx.CollectRows(rows, pgx.RowToStructByPos[connections.Column])
	if err != nil {
		return nil, err
	}
	ref := t
	ref.Type = relTypes[kind]
	return &connections.TableMeta{
		Ref:        ref,
		Columns:    cols,
		Comment:    comment,
		Format:     "postgresql",
		Properties: map[string]string{"relkind": kind},
	}, nil
}

// WalkCatalog reads every table and view of a database with its columns in
// one query, in a read-only transaction with a longer statement timeout.
func (r *reader) WalkCatalog(ctx context.Context, catalog string, visit func(connections.TableMeta) error) error {
	c, err := r.conn(ctx, catalog)
	if err != nil {
		return err
	}
	tx, err := c.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", connections.WalkTimeout.Milliseconds())); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT n.nspname, c.relname, c.relkind::text, coalesce(obj_description(c.oid, 'pg_class'), ''),
		       coalesce(a.attname, ''), coalesce(format_type(a.atttypid, a.atttypmod), ''), coalesce(NOT a.attnotnull, true),
		       coalesce(col_description(a.attrelid, a.attnum), ''), coalesce(a.attnum::int, 0)
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p', 'f', 'v', 'm') AND NOT c.relispartition
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
		  AND has_schema_privilege(n.oid, 'USAGE')
		ORDER BY n.nspname, c.relname, a.attnum`)
	if err != nil {
		return err
	}
	defer rows.Close()
	g := connections.NewTableGrouper(catalog, visit)
	for rows.Next() {
		var schema, kind string
		var row connections.CatalogRow
		if err := rows.Scan(&schema, &row.Table, &kind, &row.Comment,
			&row.Column.Name, &row.Column.NativeType, &row.Column.Nullable, &row.Column.Comment, &row.Column.Position); err != nil {
			return err
		}
		row.Namespace = connections.Namespace{schema}
		row.Type, row.Format = relTypes[kind], "postgresql"
		row.Properties = map[string]string{"relkind": kind}
		if err := g.Add(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return g.Flush()
}

func (r *reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for db, c := range r.conns {
		errs = append(errs, c.Close(context.Background()))
		delete(r.conns, db)
	}
	return errors.Join(errs...)
}
