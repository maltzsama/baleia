package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// systemDatabases are hidden from metadata listings.
var systemDatabases = []string{"system", "INFORMATION_SCHEMA", "information_schema"}

// introspector lists what a ClickHouse server exposes.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(context.Context) ([]string, error) {
	return []string{Catalog}, nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := in.e.c.queryStrings(ctx, request{
		sql: `SELECT name FROM system.databases
			WHERE name NOT IN ({s1:String}, {s2:String}, {s3:String})
			ORDER BY name`,
		params: map[string]string{"s1": systemDatabases[0], "s2": systemDatabases[1], "s3": systemDatabases[2]},
	})
	if err != nil {
		return nil, err
	}
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r[0]})
	}
	return out, nil
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(schema) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a namespace is one database")
	}
	rows, err := in.e.c.queryStrings(ctx, request{
		sql: `SELECT name, engine FROM system.tables
			WHERE database = {db:String} AND NOT is_temporary
			ORDER BY name`,
		params: map[string]string{"db": schema[0]},
	})
	if err != nil {
		return nil, err
	}
	out := []connections.TableRef{}
	for _, r := range rows {
		out = append(out, connections.TableRef{
			Catalog: catalog, Namespace: connections.Namespace{schema[0]}, Name: r[0], Type: tableType(r[1]),
		})
	}
	return out, nil
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "a namespace is one database")
	}
	params := map[string]string{"db": t.Namespace[0], "table": t.Name}
	tables, err := in.e.c.queryStrings(ctx, request{
		sql:    `SELECT engine, comment FROM system.tables WHERE database = {db:String} AND name = {table:String}`,
		params: params,
	})
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	rows, err := in.e.c.queryStrings(ctx, request{
		sql: `SELECT name, type, comment, position FROM system.columns
			WHERE database = {db:String} AND table = {table:String}
			ORDER BY position`,
		params: params,
	})
	if err != nil {
		return nil, err
	}
	cols := make([]connections.Column, 0, len(rows))
	for _, r := range rows {
		pos, _ := strconv.Atoi(r[3])
		cols = append(cols, connections.Column{
			Name: r[0], NativeType: r[1], Comment: r[2], Position: pos,
			Nullable: strings.HasPrefix(r[1], "Nullable("),
		})
	}
	engine := tables[0][0]
	ref := t
	ref.Type = tableType(engine)
	return &connections.TableMeta{
		Ref: ref, Columns: cols, Comment: tables[0][1],
		Format: strings.ToLower(engine), Properties: map[string]string{"engine": engine},
	}, nil
}

func tableType(engine string) string {
	switch engine {
	case "View":
		return "VIEW"
	case "MaterializedView":
		return "MATERIALIZED_VIEW"
	default:
		return "TABLE"
	}
}

func checkCatalog(catalog string) error {
	if catalog != Catalog {
		return queryengine.NewError(queryengine.CodeNotFound, Kind, "", fmt.Sprintf("this connection sees only the %q catalog", Catalog))
	}
	return nil
}
