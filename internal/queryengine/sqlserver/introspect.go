package sqlserver

import (
	"context"
	"fmt"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// systemDatabases are hidden from metadata listings.
var systemDatabases = []string{"master", "tempdb", "model", "msdb"}

var tableTypes = map[string]string{"BASE TABLE": "TABLE", "VIEW": "VIEW"}

// introspector lists what SQL Server sees for the engine's connection.
// Databases are reached through three-part names, so the catalog argument
// selects the database.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(ctx context.Context) ([]string, error) {
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT name FROM sys.databases
		WHERE HAS_DBACCESS(name) = 1 AND state = 0 AND name NOT IN (@p1, @p2, @p3, @p4)
		ORDER BY name`, systemDatabases[0], systemDatabases[1], systemDatabases[2], systemDatabases[3])
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, mapError(err)
		}
		out = append(out, name)
	}
	return out, mapError(rows.Err())
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	rows, err := in.e.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.name FROM %s.sys.schemas s
		JOIN %s.sys.database_principals p ON p.principal_id = s.principal_id
		WHERE s.name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest')
		  AND s.name NOT LIKE 'db[_]%%'
		ORDER BY s.name`, quoteIdent(catalog), quoteIdent(catalog)))
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
	if len(schema) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	rows, err := in.e.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT TABLE_NAME, TABLE_TYPE FROM %s.INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = @p1 ORDER BY TABLE_NAME`, quoteIdent(catalog)), schema[0])
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
	if len(t.Namespace) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	db := quoteIdent(t.Catalog)
	rows, err := in.e.db.QueryContext(ctx, fmt.Sprintf(`
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
		return nil, mapError(err)
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		var nullable int
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &c.Comment, &c.Position); err != nil {
			return nil, mapError(err)
		}
		c.Nullable = nullable == 1
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(cols) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	return &connections.TableMeta{Ref: t, Columns: cols, Format: "sqlserver"}, nil
}
