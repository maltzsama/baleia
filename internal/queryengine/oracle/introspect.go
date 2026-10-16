package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// introspector lists what Oracle sees for the engine's connection. The catalog
// is the service the connection opened; the schemas are the non-Oracle-
// maintained users that own objects.
type introspector struct{ e *Engine }

func (in introspector) checkCatalog(catalog string) error {
	if !strings.EqualFold(catalog, in.e.catalog) {
		return queryengine.NewError(queryengine.CodeNotFound, Kind, "", fmt.Sprintf("this connection sees only the %q service", in.e.catalog))
	}
	return nil
}

func (in introspector) ListCatalogs(context.Context) ([]string, error) {
	return []string{in.e.catalog}, nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	if err := in.checkCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT username FROM all_users
		WHERE oracle_maintained = 'N'
		  AND EXISTS (SELECT 1 FROM all_objects o WHERE o.owner = all_users.username AND o.object_type IN ('TABLE', 'VIEW'))
		ORDER BY username`)
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
	if err := in.checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(schema) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	rows, err := in.e.db.QueryContext(ctx, `
		SELECT name, kind FROM (
			SELECT table_name AS name, 'TABLE' AS kind FROM all_tables WHERE owner = :1
			UNION ALL
			SELECT view_name, 'VIEW' FROM all_views WHERE owner = :2
		) ORDER BY name`, schema[0], schema[0])
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []connections.TableRef{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, mapError(err)
		}
		out = append(out, connections.TableRef{
			Catalog: catalog, Namespace: connections.Namespace{schema[0]}, Name: name, Type: kind,
		})
	}
	return out, mapError(rows.Err())
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := in.checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a schema is one level")
	}
	rows, err := in.e.db.QueryContext(ctx, `
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
		return nil, mapError(err)
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var c connections.Column
		var nullable string
		var comment sql.NullString // Oracle: '' is NULL here
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &comment, &c.Position); err != nil {
			return nil, mapError(err)
		}
		c.Nullable = nullable == "Y"
		c.Comment = comment.String
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(cols) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	return &connections.TableMeta{Ref: t, Columns: cols, Format: "oracle"}, nil
}
