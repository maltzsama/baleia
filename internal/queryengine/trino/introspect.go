package trino

import (
	"context"
	"fmt"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// introspector lists what Trino sees, as the service identity. Identifiers
// are quoted; values are literals with quotes doubled. #14 moves these
// queries into the dialect.
type introspector struct{ e *Engine }

func (in introspector) query(ctx context.Context, sql string) ([][]any, error) {
	res, err := in.e.client.ExecuteWithOptions(ctx, sql, trinoclient.ExecuteOptions{Source: "baleia", ClientTags: []string{"baleia", "introspection"}})
	if err != nil {
		return nil, mapError(err)
	}
	return res.Data, nil
}

func (in introspector) ListCatalogs(ctx context.Context) ([]string, error) {
	rows, err := in.query(ctx, "SHOW CATALOGS")
	if err != nil {
		return nil, err
	}
	return firstColumn(rows), nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	c, err := in.e.dialect.QualifiedName(catalog)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	}
	rows, err := in.query(ctx, "SHOW SCHEMAS FROM "+c)
	if err != nil {
		return nil, err
	}
	out := [][]string{}
	for _, s := range firstColumn(rows) {
		out = append(out, []string{s})
	}
	return out, nil
}

func schemaName(schema []string) (string, error) {
	if len(schema) != 1 {
		return "", queryengine.NewError(queryengine.CodeNotFound, Kind, "", "trino schemas have one level")
	}
	return schema[0], nil
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	s, err := schemaName(schema)
	if err != nil {
		return nil, err
	}
	c, err := in.e.dialect.QualifiedName(catalog)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	}
	rows, err := in.query(ctx, fmt.Sprintf(
		"SELECT table_name, table_type FROM %s.information_schema.tables WHERE table_schema = %s ORDER BY table_name",
		c, in.e.dialect.QuoteString(s)))
	if err != nil {
		return nil, err
	}
	out := make([]connections.TableRef, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		typ := "TABLE"
		if fmt.Sprint(r[1]) == "VIEW" {
			typ = "VIEW"
		}
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: connections.Namespace{s}, Name: fmt.Sprint(r[0]), Type: typ})
	}
	return out, nil
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	s, err := schemaName(t.Namespace)
	if err != nil {
		return nil, err
	}
	c, err := in.e.dialect.QualifiedName(t.Catalog)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	}
	rows, err := in.query(ctx, fmt.Sprintf(
		"SELECT column_name, data_type, is_nullable, ordinal_position FROM %s.information_schema.columns WHERE table_schema = %s AND table_name = %s ORDER BY ordinal_position",
		c, in.e.dialect.QuoteString(s), in.e.dialect.QuoteString(t.Name)))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	meta := &connections.TableMeta{Ref: t}
	for i, r := range rows {
		if len(r) < 3 {
			continue
		}
		meta.Columns = append(meta.Columns, connections.Column{
			Name:       fmt.Sprint(r[0]),
			NativeType: fmt.Sprint(r[1]),
			Nullable:   fmt.Sprint(r[2]) == "YES",
			Position:   i + 1,
		})
	}
	return meta, nil
}

func firstColumn(rows [][]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 {
			out = append(out, fmt.Sprint(r[0]))
		}
	}
	return out
}
