package sqlite

import (
	"context"
	"fmt"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// Catalog is the only catalog of a SQLite database.
const Catalog = "main"

// introspector lists what a SQLite file exposes: one catalog, no schemas.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(context.Context) ([]string, error) {
	return []string{Catalog}, nil
}

// ListSchemas returns the single, unnamed namespace: SQLite has no schemas.
func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	return [][]string{{}}, nil
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(schema) != 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "SQLite has no schemas")
	}
	rows, err := in.e.ro.QueryContext(ctx, `
		SELECT name, type FROM sqlite_schema
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY name`)
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
			Catalog: catalog, Namespace: connections.Namespace{}, Name: name, Type: tableType(typ),
		})
	}
	return out, mapError(rows.Err())
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "SQLite has no schemas")
	}
	var typ string
	err := in.e.ro.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE type IN ('table', 'view') AND name = ?`, t.Name).Scan(&typ)
	if err != nil {
		return nil, mapError(err)
	}
	rows, err := in.e.ro.QueryContext(ctx, `SELECT name, type, "notnull", cid FROM pragma_table_info(?) ORDER BY cid`, t.Name)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		var notnull, cid int
		if err := rows.Scan(&c.Name, &c.NativeType, &notnull, &cid); err != nil {
			return nil, mapError(err)
		}
		c.Nullable = notnull == 0
		c.Position = cid + 1
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(cols) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	ref := t
	ref.Type = tableType(typ)
	return &connections.TableMeta{Ref: ref, Columns: cols, Format: "sqlite", Properties: map[string]string{}}, nil
}

func tableType(sqliteType string) string {
	if sqliteType == "view" {
		return "VIEW"
	}
	return "TABLE"
}

func checkCatalog(catalog string) error {
	if catalog != Catalog {
		return queryengine.NewError(queryengine.CodeNotFound, Kind, "", fmt.Sprintf("this connection sees only the %q catalog", Catalog))
	}
	return nil
}
