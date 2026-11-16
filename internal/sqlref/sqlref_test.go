package sqlref

import (
	"reflect"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestExtractTables(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sql  string
		want []Table
	}{
		{
			"simple select",
			"SELECT * FROM orders",
			[]Table{{Path: []string{"orders"}, Alias: "orders", Kind: "read"}},
		},
		{
			"aliases and schema",
			"SELECT * FROM sales.orders o WHERE o.id > 1",
			[]Table{{Path: []string{"sales", "orders"}, Alias: "o", Kind: "read"}},
		},
		{
			"quoted names",
			`SELECT * FROM "my schema"."my table" AS t`,
			[]Table{{Path: []string{"my schema", "my table"}, Alias: "t", Kind: "read"}},
		},
		{
			"multiple joins",
			"SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id JOIN items i ON i.order_id = o.id",
			[]Table{
				{Path: []string{"orders"}, Alias: "o", Kind: "read"},
				{Path: []string{"customers"}, Alias: "c", Kind: "read"},
				{Path: []string{"items"}, Alias: "i", Kind: "read"},
			},
		},
		{
			"write table",
			"INSERT INTO target SELECT * FROM source",
			[]Table{
				{Path: []string{"target"}, Alias: "target", Kind: "write"},
				{Path: []string{"source"}, Alias: "source", Kind: "read"},
			},
		},
		{
			"derived table keeps its base table",
			"SELECT * FROM (SELECT * FROM orders) x",
			[]Table{{Path: []string{"orders"}, Alias: "orders", Kind: "read"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Extract(tc.sql).Tables; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tables = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestExtractJoins(t *testing.T) {
	t.Parallel()
	sql := `SELECT * FROM orders o
		JOIN customers c ON o.customer_id = c.id
		JOIN items i ON i.order_id = o.id AND i.sku = 'x'`
	got := Extract(sql).Joins
	want := []Join{
		{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"},
		{LeftAlias: "i", LeftColumn: "order_id", RightAlias: "o", RightColumn: "id"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Joins = %#v, want %#v", got, want)
	}
}

func TestExtractColumnsFromAST(t *testing.T) {
	t.Parallel()
	got := Extract(`SELECT o.id, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE c.active = true`)
	want := []Column{
		{Table: "orders", Name: "id"},
		{Table: "customers", Name: "name"},
		{Table: "orders", Name: "customer_id"},
		{Table: "customers", Name: "id"},
		{Table: "customers", Name: "active"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

func TestExtractResolvedColumns(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT o.id FROM orders o WHERE o.id = 1")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedColumnsThroughCTE: a column qualified by a CTE or derived
// table resolves to the physical table the source reads, not the alias.
func TestExtractResolvedColumnsThroughCTE(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	for _, sql := range []string{
		"WITH x AS (SELECT id FROM orders) SELECT x.id FROM x",
		"SELECT q.id FROM (SELECT id FROM orders) q",
	} {
		got := ExtractResolved(d, "sales", sql)
		want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
		if !reflect.DeepEqual(got.Columns, want) {
			t.Fatalf("%s: Columns = %#v, want %#v", sql, got.Columns, want)
		}
	}
}

// TestExtractResolvedSkipsOutputAlias: a reference to a projection alias is not
// a physical column.
func TestExtractResolvedSkipsOutputAlias(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT o.id AS display_id FROM orders o ORDER BY display_id")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

func TestExtractResolvedQualifiesSources(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	cases := []struct {
		name string
		sql  string
		want []Table
	}{
		{
			"default schema stamps an unqualified table",
			"SELECT * FROM orders",
			[]Table{{Path: []string{"sales", "orders"}, Alias: "orders", Kind: "read"}},
		},
		{
			"explicit schema wins over the default",
			"SELECT * FROM other.orders",
			[]Table{{Path: []string{"other", "orders"}, Alias: "orders", Kind: "read"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractResolved(d, "sales", tc.sql).Tables; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tables = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestExtractResolvedFollowsNestedSources(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	cases := []struct {
		name string
		sql  string
		want []Table
	}{
		{
			"cte resolves to its physical source",
			"WITH x AS (SELECT * FROM orders) SELECT * FROM x",
			[]Table{{Path: []string{"orders"}, Alias: "orders", Kind: "read"}},
		},
		{
			"derived table resolves to its base table",
			"SELECT * FROM (SELECT * FROM orders) q",
			[]Table{{Path: []string{"orders"}, Alias: "orders", Kind: "read"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractWithDialect(d, tc.sql).Tables; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tables = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestExtractResolvedWriteTarget(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "INSERT INTO target SELECT * FROM source")
	want := []Table{
		{Path: []string{"sales", "target"}, Alias: "target", Kind: "write"},
		{Path: []string{"sales", "source"}, Alias: "source", Kind: "read"},
	}
	if !reflect.DeepEqual(got.Tables, want) {
		t.Fatalf("Tables = %#v, want %#v", got.Tables, want)
	}
}

// TestExtractResolvedWriteTargetColumnList: an INSERT with a column list still
// classifies its target as a write (the target is wrapped in a Schema node).
func TestExtractResolvedWriteTargetColumnList(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "INSERT INTO target(id) SELECT id FROM source")
	if len(got.Tables) != 2 || got.Tables[0].Kind != "write" || got.Tables[0].Path[len(got.Tables[0].Path)-1] != "target" {
		t.Fatalf("Tables = %#v", got.Tables)
	}
}

func TestExtractIgnoresLiteralsAndUnknownAliases(t *testing.T) {
	t.Parallel()
	sql := "SELECT * FROM orders o JOIN customers c ON o.id = 5 AND x = y AND o.a = missing.b"
	if got := Extract(sql).Joins; len(got) != 0 {
		t.Fatalf("Joins = %#v, want none", got)
	}
}

func TestExtractUnparsableIsEmpty(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{"", "SELECT FROM (((", "'''", "/* open"} {
		_ = Extract(sql) // must not panic
	}
}
