package mysql

import (
	"context"
	"fmt"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Catalog is the only catalog of a MySQL server: information_schema reports
// every schema under CATALOG_NAME 'def'.
const Catalog = "def"

// systemSchemas are hidden from metadata listings.
var systemSchemas = []string{"mysql", "information_schema", "performance_schema", "sys"}

var tableTypes = map[string]string{"BASE TABLE": "TABLE", "VIEW": "VIEW", "SYSTEM VIEW": "VIEW"}

// introspector lists what MySQL sees for the engine's connection.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(context.Context) ([]string, error) {
	return []string{Catalog}, nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
		WHERE SCHEMA_NAME NOT IN (?, ?, ?, ?)
		ORDER BY SCHEMA_NAME`, systemSchemas[0], systemSchemas[1], systemSchemas[2], systemSchemas[3])
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := [][]string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, mapError(err)
		}
		out = append(out, []string{name})
	}
	return out, mapError(rows.Err())
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(schema) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, KindMySQL, "", "a schema is one level")
	}
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, schema[0])
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []connections.TableRef{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, mapError(err)
		}
		out = append(out, connections.TableRef{
			Catalog: catalog, Namespace: connections.Namespace{schema[0]}, Name: name, Type: tableTypes[typ],
		})
	}
	return out, mapError(rows.Err())
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, KindMySQL, "", "a schema is one level")
	}
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_COMMENT, ORDINAL_POSITION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, t.Namespace[0], t.Name)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		if err := rows.Scan(&c.Name, &c.NativeType, &c.Nullable, &c.Comment, &c.Position); err != nil {
			return nil, mapError(err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(cols) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, KindMySQL, "", "table not found")
	}
	return &connections.TableMeta{Ref: t, Columns: cols, Format: "mysql"}, nil
}

func checkCatalog(catalog string) error {
	if catalog != Catalog {
		return queryengine.NewError(queryengine.CodeNotFound, KindMySQL, "", fmt.Sprintf("this connection sees only the %q catalog", Catalog))
	}
	return nil
}
