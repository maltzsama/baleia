package flightsql

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// catalogs lists the server's catalogs.
func (c *client) catalogs(ctx context.Context) ([]string, error) {
	var out []string
	err := c.fetch(ctx,
		func(ctx context.Context) (*flight.FlightInfo, error) { return c.client.GetCatalogs(ctx) },
		func(rec arrow.RecordBatch) error { out = append(out, column(rec, "catalog_name")...); return nil })
	return out, err
}

// catalogFilter maps a Baleia catalog to the protocol filter: a server without
// catalogs is "no filter".
func catalogFilter(catalog string) *string {
	if catalog == DefaultCatalog {
		return nil
	}
	return &catalog
}

// schemaOf is the protocol's schema of a namespace ("" for the unnamed one).
func schemaOf(ns []string) (string, error) {
	switch len(ns) {
	case 0:
		return "", nil
	case 1:
		return ns[0], nil
	}
	return "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
}

type table struct {
	name, kind string
	schema     []byte
}

// tables lists the tables of one schema. Filter patterns use LIKE wildcards
// without an escape, so names are matched exactly here as well.
func (c *client) tables(ctx context.Context, catalog, schema string, name *string, withSchema bool) ([]table, error) {
	opts := &flightsql.GetTablesOpts{Catalog: catalogFilter(catalog), TableNameFilterPattern: name, IncludeSchema: withSchema}
	if schema != "" {
		opts.DbSchemaFilterPattern = &schema
	}
	var out []table
	err := c.fetch(ctx,
		func(ctx context.Context) (*flight.FlightInfo, error) { return c.client.GetTables(ctx, opts) },
		func(rec arrow.RecordBatch) error {
			schemas, names, kinds := column(rec, "db_schema_name"), column(rec, "table_name"), column(rec, "table_type")
			var blobs binaryColumn
			if idx := rec.Schema().FieldIndices("table_schema"); withSchema && len(idx) > 0 {
				blobs, _ = rec.Column(idx[0]).(binaryColumn)
			}
			for i := range names {
				if schemas[i] != schema || (name != nil && names[i] != *name) {
					continue
				}
				t := table{name: names[i], kind: kinds[i]}
				if blobs != nil && blobs.IsValid(i) {
					t.schema = slices.Clone(blobs.Value(i))
				}
				out = append(out, t)
			}
			return nil
		})
	return out, err
}

func refType(kind string) string {
	if strings.Contains(strings.ToUpper(kind), "VIEW") {
		return "VIEW"
	}
	return "TABLE"
}

// introspector lists what a Flight SQL server exposes through the protocol.
type introspector struct{ e *Engine }

func (in introspector) ListCatalogs(ctx context.Context) ([]string, error) {
	cats, err := in.e.c.catalogs(ctx)
	if err != nil {
		return nil, err
	}
	if len(cats) == 0 {
		return []string{DefaultCatalog}, nil
	}
	return cats, nil
}

func (in introspector) ListSchemas(ctx context.Context, catalog string) ([][]string, error) {
	var names []string
	err := in.e.c.fetch(ctx,
		func(ctx context.Context) (*flight.FlightInfo, error) {
			return in.e.c.client.GetDBSchemas(ctx, &flightsql.GetDBSchemasOpts{Catalog: catalogFilter(catalog)})
		},
		func(rec arrow.RecordBatch) error { names = append(names, column(rec, "db_schema_name")...); return nil })
	if err != nil {
		return nil, err
	}
	// A server without schemas has its tables in one unnamed namespace.
	if len(names) == 0 {
		return [][]string{{}}, nil
	}
	slices.Sort(names)
	out := make([][]string, 0, len(names))
	for _, n := range slices.Compact(names) {
		out = append(out, []string{n})
	}
	return out, nil
}

func (in introspector) ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error) {
	ns, err := schemaOf(schema)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	}
	all, err := in.e.c.tables(ctx, catalog, ns, nil, false)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b table) int { return strings.Compare(a.name, b.name) })
	out := make([]connections.TableRef, 0, len(all))
	for _, t := range all {
		out = append(out, connections.TableRef{
			Catalog: catalog, Namespace: connections.Namespace(schema), Name: t.name, Type: refType(t.kind),
		})
	}
	return out, nil
}

func (in introspector) DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	ns, err := schemaOf(t.Namespace)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
	}
	name := t.Name
	found, err := in.e.c.tables(ctx, t.Catalog, ns, &name, true)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, queryengine.NewError(queryengine.CodeNotFound, Kind, "", "table not found")
	}
	ref := t
	ref.Type = refType(found[0].kind)
	meta := &connections.TableMeta{
		Ref: ref, Columns: []connections.Column{}, Format: "flightsql",
		Properties: map[string]string{"table_type": found[0].kind},
	}
	if found[0].schema == nil {
		return meta, nil
	}
	schema, err := flight.DeserializeSchema(found[0].schema, memory.DefaultAllocator)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeInternal, Kind, "", "flight sql table schema: "+err.Error())
	}
	for i, f := range schema.Fields() {
		native := f.Type.String()
		md := flightsql.ColumnMetadata{Data: &f.Metadata}
		if tn, ok := md.TypeName(); ok && tn != "" {
			native = tn
		}
		meta.Columns = append(meta.Columns, connections.Column{
			Name: f.Name, NativeType: native, Nullable: f.Nullable, Position: i + 1,
		})
	}
	return meta, nil
}
