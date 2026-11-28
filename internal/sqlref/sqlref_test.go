package sqlref

import (
	"reflect"
	"testing"

	"github.com/maltzsama/baleia/internal/dialect"
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

// TestExtractResolvedColumnsPerScope: a column resolves against the scope that
// contains it, so an alias reused in another scope does not merge, and a
// column read through a CTE or derived table reaches the physical column behind
// it (#1089).
func TestExtractResolvedColumnsPerScope(t *testing.T) {
	t.Parallel()
	trino := dialect.NewRegistry().MustGet(dialect.Trino)
	cases := []struct {
		name string
		sql  string
		want []Column
	}{
		{
			"alias reused in a subquery",
			"SELECT a.id FROM orders a WHERE EXISTS (SELECT a.name FROM customers a)",
			[]Column{{Namespace: "sales", Table: "orders", Name: "id"}, {Namespace: "sales", Table: "customers", Name: "name"}},
		},
		{
			"correlated reference to the enclosing query",
			"SELECT o.id FROM orders o WHERE EXISTS (SELECT 1 FROM customers c WHERE c.owner = o.id)",
			[]Column{{Namespace: "sales", Table: "orders", Name: "id"}, {Namespace: "sales", Table: "customers", Name: "owner"}},
		},
		{
			"nested ctes",
			"WITH y AS (SELECT id FROM orders), x AS (SELECT id FROM y) SELECT x.id FROM x",
			[]Column{{Namespace: "sales", Table: "orders", Name: "id"}},
		},
		{
			"cte projection alias maps to its physical column",
			"WITH x AS (SELECT customer_id AS cid FROM orders) SELECT x.cid FROM x",
			[]Column{{Namespace: "sales", Table: "orders", Name: "customer_id"}},
		},
		{
			"cte column list names outputs by position",
			"WITH x(b, a) AS (SELECT a, b FROM orders) SELECT x.a FROM x",
			[]Column{{Namespace: "sales", Table: "orders", Name: "b"}, {Namespace: "sales", Table: "orders", Name: "a"}},
		},
		{
			"star passes a name through to its source",
			"WITH x AS (SELECT * FROM orders) SELECT y.customer_id FROM x y",
			[]Column{{Namespace: "sales", Table: "orders", Name: "customer_id"}},
		},
		{
			"computed projection is not a physical column",
			"WITH x AS (SELECT count(*) AS n FROM orders) SELECT x.n FROM x",
			[]Column{{Name: "n"}},
		},
		{
			"projection alias of an outer scope does not hide an inner column",
			"SELECT 1 AS status WHERE EXISTS (SELECT status FROM shipments)",
			[]Column{{Namespace: "sales", Table: "shipments", Name: "status"}},
		},
		{
			"unqualified column with two sources stays unresolved",
			"SELECT 1 FROM orders o JOIN customers c ON o.id = c.order_id WHERE flag = 1",
			[]Column{{Namespace: "sales", Table: "orders", Name: "id"}, {Namespace: "sales", Table: "customers", Name: "order_id"}, {Name: "flag"}},
		},
		{
			"unqualified column does not bind to an enclosing query",
			"SELECT 1 FROM orders WHERE EXISTS (SELECT 1 FROM customers c, items i WHERE order_flag = 1)",
			[]Column{{Name: "order_flag"}},
		},
		{
			"derived table does not see its sibling sources",
			"SELECT 1 FROM orders o WHERE EXISTS (SELECT 1 FROM customers o, (SELECT o.id) q)",
			[]Column{{Namespace: "sales", Table: "orders", Name: "id"}},
		},
		{
			"an inherited cte does not shadow an outer alias",
			"WITH o AS (SELECT id AS customer_id FROM shipments) SELECT 1 FROM orders o WHERE EXISTS (SELECT 1 FROM customers c WHERE c.id = o.customer_id)",
			[]Column{
				{Namespace: "sales", Table: "shipments", Name: "id"},
				{Namespace: "sales", Table: "customers", Name: "id"},
				{Namespace: "sales", Table: "orders", Name: "customer_id"},
			},
		},
		{
			"projection aliases in group by, having and order by are not columns",
			"SELECT o.customer_id AS cid, count(*) AS n FROM orders o GROUP BY cid HAVING n > 1 ORDER BY cid",
			[]Column{{Namespace: "sales", Table: "orders", Name: "customer_id"}},
		},
		{
			"a where name matching a projection alias is not rewritten to the aliased column",
			"SELECT o.customer_id AS id FROM orders o CROSS JOIN customers c WHERE id = c.customer_key",
			[]Column{{Namespace: "sales", Table: "orders", Name: "customer_id"}, {Namespace: "sales", Table: "customers", Name: "customer_key"}},
		},
		{
			"union branches resolve to their own tables",
			"SELECT id FROM a UNION ALL SELECT id FROM b",
			[]Column{{Namespace: "sales", Table: "a", Name: "id"}, {Namespace: "sales", Table: "b", Name: "id"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractResolved(trino, "sales", tc.sql).Columns; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Columns = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestExtractResolvedJoinsPerScope: an equi-join through a CTE or derived table
// is kept and named after the physical table and column behind each endpoint
// (#1089).
func TestExtractResolvedJoinsPerScope(t *testing.T) {
	t.Parallel()
	registry := dialect.NewRegistry()
	trino := registry.MustGet(dialect.Trino)
	cases := []struct {
		name    string
		dialect dialect.Dialect
		sql     string
		want    []Join
	}{
		{
			"cte",
			trino,
			"WITH x AS (SELECT customer_id FROM orders) SELECT * FROM x JOIN customers c ON x.customer_id = c.id",
			[]Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"cte over an aliased table",
			trino,
			"WITH x AS (SELECT o.customer_id FROM orders o) SELECT * FROM x JOIN customers c ON x.customer_id = c.id",
			[]Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"cte projection alias",
			trino,
			"WITH x AS (SELECT customer_id AS cid FROM orders) SELECT * FROM x JOIN customers c ON x.cid = c.id",
			[]Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"derived table",
			trino,
			"SELECT * FROM (SELECT id FROM orders) q JOIN customers c ON q.id = c.id",
			[]Join{{LeftAlias: "orders", LeftColumn: "id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"qualified star in a derived table",
			trino,
			"SELECT * FROM (SELECT o.* FROM orders o) q JOIN customers c ON q.customer_id = c.id",
			[]Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"join inside a nested scope",
			trino,
			"SELECT 1 FROM orders o WHERE EXISTS (SELECT 1 FROM customers c JOIN items i ON c.id = i.customer_id)",
			[]Join{{LeftAlias: "c", LeftColumn: "id", RightAlias: "i", RightColumn: "customer_id"}},
		},
		{
			"cte reading two tables",
			trino,
			"WITH x AS (SELECT o.id FROM orders o JOIN items i ON o.id = i.order_id) SELECT * FROM x JOIN customers c ON x.id = c.order_id",
			[]Join{
				{LeftAlias: "o", LeftColumn: "id", RightAlias: "c", RightColumn: "order_id"},
				{LeftAlias: "o", LeftColumn: "id", RightAlias: "i", RightColumn: "order_id"},
			},
		},
		{
			"semi join",
			trino,
			"SELECT * FROM orders o LEFT SEMI JOIN customers c ON o.customer_id = c.id",
			[]Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"correlated endpoint skips an inherited cte",
			trino,
			"WITH o AS (SELECT id AS customer_id FROM shipments) SELECT 1 FROM orders o WHERE EXISTS (SELECT 1 FROM customers c WHERE c.id = o.customer_id)",
			[]Join{{LeftAlias: "c", LeftColumn: "id", RightAlias: "o", RightColumn: "customer_id"}},
		},
		{
			"where name matching a projection alias is not a join",
			trino,
			"SELECT o.customer_id AS id FROM orders o CROSS JOIN customers c WHERE id = c.customer_key",
			[]Join{},
		},
		{
			"alias naming two tables is skipped",
			trino,
			"WITH x AS (SELECT o.id FROM orders o), y AS (SELECT o.id FROM customers o) SELECT * FROM x JOIN y ON x.id = y.id",
			[]Join{},
		},
		{
			"projection alias in having is not a join endpoint",
			trino,
			"SELECT o.id, count(*) AS n FROM orders o GROUP BY o.id HAVING n = o.id",
			[]Join{},
		},
		{
			"computed endpoint is skipped",
			trino,
			"WITH x AS (SELECT max(customer_id) AS cid FROM orders) SELECT * FROM x JOIN customers c ON x.cid = c.id",
			[]Join{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractResolved(tc.dialect, "sales", tc.sql).Joins; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Joins = %#v, want %#v", got, tc.want)
			}
		})
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
