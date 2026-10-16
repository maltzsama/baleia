package postgresql

import (
	"context"
	"fmt"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
)

// introspector lists what PostgreSQL sees for the engine's connection. The
// catalog is the connection's own database: a session cannot switch database.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(ctx context.Context) ([]string, error) {
	rows, err := in.e.pool.Query(ctx, `
		SELECT datname FROM pg_catalog.pg_database
		WHERE datallowconn AND NOT datistemplate AND has_database_privilege(datname, 'CONNECT')
		ORDER BY datname`)
	if err != nil {
		return nil, mapError(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, mapError(err)
	}
	return names, nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	if err := in.checkCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := in.e.pool.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'pg_toast')
		  AND schema_name NOT LIKE 'pg\_temp\_%' AND schema_name NOT LIKE 'pg\_toast\_temp\_%'
		ORDER BY schema_name`)
	if err != nil {
		return nil, mapError(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, mapError(err)
	}
	out := make([][]string, 0, len(names))
	for _, n := range names {
		out = append(out, []string{n})
	}
	return out, nil
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	if err := in.checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(schema) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	rows, err := in.e.pool.Query(ctx, `
		SELECT table_name, CASE WHEN table_type = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END
		FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name`, schema[0])
	if err != nil {
		return nil, mapError(err)
	}
	type rel struct {
		Name string
		Type string
	}
	rels, err := pgx.CollectRows(rows, pgx.RowToStructByPos[rel])
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]connections.TableRef, 0, len(rels))
	for _, r := range rels {
		out = append(out, connections.TableRef{
			Catalog: catalog, Namespace: connections.Namespace{schema[0]}, Name: r.Name, Type: r.Type,
		})
	}
	return out, nil
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := in.checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	rows, err := in.e.pool.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
		       coalesce(col_description(a.attrelid, a.attnum), ''), a.attnum::int
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, t.Namespace[0], t.Name)
	if err != nil {
		return nil, mapError(err)
	}
	cols, err := pgx.CollectRows(rows, pgx.RowToStructByPos[connections.Column])
	if err != nil {
		return nil, mapError(err)
	}
	if len(cols) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	return &connections.TableMeta{Ref: t, Columns: cols, Format: "postgresql"}, nil
}

func (in introspector) checkCatalog(catalog string) error {
	if catalog != in.e.database {
		return queryengine.NewError(queryengine.CodeNotFound, Kind, "", fmt.Sprintf("this connection sees only the %q database", in.e.database))
	}
	return nil
}
