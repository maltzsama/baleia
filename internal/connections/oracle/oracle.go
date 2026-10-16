// Package oracle is the Oracle Database provider. Metadata is read from the
// ALL_* dictionary views with sijms/go-ora (pure Go); every connection is
// dialed through connections.Dialer (OracleConnector.Dialer).
package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	goora "github.com/sijms/go-ora/v2"
)

const (
	statementTimeout = 30 * time.Second
	pageSize         = 1000
)

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindOracle }
func (*Provider) Label() string              { return "Oracle Database" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "oracle" }

// WalksCatalogs: the reader reads a whole catalog in one query (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "Port", Type: connections.FieldInt, Default: "1521"},
		{Key: "service_name", Label: "Service name", Type: connections.FieldText, Required: true, Help: "e.g. FREEPDB1"},
		{Key: "cancel_with_alter_system", Label: "Cancel statements with ALTER SYSTEM", Type: connections.FieldBool, Default: "false", Help: "Requires the ALTER SYSTEM permission; lets a running statement be cancelled from another process"},
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
	for _, k := range []string{"host", "service_name"} {
		if strings.ContainsAny(out.Values[k], "/ ,()=@") {
			return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: k, Message: "contains characters that are not allowed"}}}
		}
	}
	return out, nil
}

func (p *Provider) open(cfg connections.Config) (*sql.DB, error) {
	port, err := strconv.Atoi(valueOr(cfg.Value("port"), "1521"))
	if err != nil {
		return nil, fmt.Errorf("oracle config: %w", connections.ErrValidation)
	}
	dsn := goora.BuildUrl(cfg.Value("host"), port, cfg.Value("service_name"), cfg.Value("username"), cfg.Secret("password"),
		map[string]string{"CONNECTION TIMEOUT": "10", "PROGRAM": "baleia"})
	c, ok := goora.NewConnector(dsn).(*goora.OracleConnector)
	if !ok {
		return nil, errors.New("oracle: unexpected connector type")
	}
	c.Dialer(p.dialer)
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
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
	return &reader{db: db, catalog: cfg.Value("service_name")}, nil
}

// reader exposes the database as one catalog (its service name) whose
// namespaces are the non-Oracle-maintained users that own objects.
type reader struct {
	db      *sql.DB
	catalog string
}

func (r *reader) checkCatalog(c string) error {
	if !strings.EqualFold(c, r.catalog) {
		return fmt.Errorf("%w: catalog", connections.ErrNotFound)
	}
	return nil
}

func (r *reader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: r.catalog, Kind: "database"}}, nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if err := r.checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT username FROM all_users
		WHERE oracle_maintained = 'N'
		  AND EXISTS (SELECT 1 FROM all_objects o WHERE o.owner = all_users.username AND o.object_type IN ('TABLE', 'VIEW'))
		ORDER BY username`)
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

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if err := r.checkCatalog(catalog); err != nil {
		return nil, "", err
	}
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	// Oracle treats '' as NULL, so "name > ''" matches nothing: the first page
	// has no lower bound at all.
	// Placeholders are numbered in the order they appear, like the args.
	after, args := "", []any{ns[0], ns[0]}
	limit := ":3"
	if pageToken != "" {
		after, limit, args = "WHERE name > :3", ":4", append(args, pageToken)
	}
	args = append(args, pageSize+1)
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, kind FROM (
			SELECT table_name AS name, 'TABLE' AS kind FROM all_tables WHERE owner = :1
			UNION ALL
			SELECT view_name, 'VIEW' FROM all_views WHERE owner = :2
		) `+after+`
		ORDER BY name
		FETCH FIRST `+limit+` ROWS ONLY`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []connections.TableRef{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, "", err
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
	if err := r.checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	var typ string
	var comment sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT table_type, comments FROM all_tab_comments WHERE owner = :1 AND table_name = :2`,
		t.Namespace[0], t.Name).Scan(&typ, &comment)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.column_name,
		       c.data_type || CASE
		           WHEN c.data_type IN ('VARCHAR2', 'NVARCHAR2', 'CHAR', 'NCHAR', 'RAW') THEN '(' || c.char_length || ')'
		           WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL THEN '(' || c.data_precision || ',' || c.data_scale || ')'
		           ELSE '' END,
		       c.nullable, cc.comments, c.column_id
		FROM all_tab_columns c
		LEFT JOIN all_col_comments cc
		       ON cc.owner = c.owner AND cc.table_name = c.table_name AND cc.column_name = c.column_name
		WHERE c.owner = :1 AND c.table_name = :2
		ORDER BY c.column_id`, t.Namespace[0], t.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		var nullable string
		var comment sql.NullString // Oracle: '' is NULL
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &comment, &c.Position); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "Y"
		c.Comment = comment.String
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ref := t
	ref.Type = typ // TABLE | VIEW
	return &connections.TableMeta{Ref: ref, Columns: cols, Comment: comment.String, Format: "oracle", Properties: map[string]string{}}, nil
}

// WalkCatalog reads every table and view of the user schemas with their
// columns in one query.
func (r *reader) WalkCatalog(ctx context.Context, catalog string, visit func(connections.TableMeta) error) error {
	if err := r.checkCatalog(catalog); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, connections.WalkTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.owner, o.name, o.kind, tc.comments,
		       c.column_name,
		       c.data_type || CASE
		           WHEN c.data_type IN ('VARCHAR2', 'NVARCHAR2', 'CHAR', 'NCHAR', 'RAW') THEN '(' || c.char_length || ')'
		           WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL THEN '(' || c.data_precision || ',' || c.data_scale || ')'
		           ELSE '' END,
		       c.nullable, cc.comments, c.column_id
		FROM (
		    SELECT owner, table_name AS name, 'TABLE' AS kind FROM all_tables
		    UNION ALL
		    SELECT owner, view_name, 'VIEW' FROM all_views
		) o
		JOIN all_users u ON u.username = o.owner AND u.oracle_maintained = 'N'
		LEFT JOIN all_tab_comments tc ON tc.owner = o.owner AND tc.table_name = o.name
		LEFT JOIN all_tab_columns c ON c.owner = o.owner AND c.table_name = o.name
		LEFT JOIN all_col_comments cc
		       ON cc.owner = c.owner AND cc.table_name = c.table_name AND cc.column_name = c.column_name
		ORDER BY o.owner, o.name, c.column_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	g := connections.NewTableGrouper(catalog, visit)
	for rows.Next() {
		var owner string
		// Oracle: '' is NULL, and a table without columns has NULL ones.
		var tableComment, column, typ, nullable, comment sql.NullString
		var position sql.NullInt64
		var row connections.CatalogRow
		if err := rows.Scan(&owner, &row.Table, &row.Type, &tableComment, &column, &typ, &nullable, &comment, &position); err != nil {
			return err
		}
		row.Namespace = connections.Namespace{owner}
		row.Comment, row.Format = tableComment.String, "oracle"
		row.Column = connections.Column{
			Name: column.String, NativeType: typ.String, Nullable: nullable.String == "Y",
			Comment: comment.String, Position: int(position.Int64),
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
