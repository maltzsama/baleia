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

// TestExtractResolvedAliasesPerScope: an alias reused in another scope
// resolves against the table of its own scope (#1089).
func TestExtractResolvedAliasesPerScope(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT a.id FROM orders a WHERE EXISTS (SELECT a.name FROM customers a)")
	want := []Column{
		{Namespace: "sales", Table: "orders", Name: "id"},
		{Namespace: "sales", Table: "customers", Name: "name"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedCorrelatedSubquery: a correlated reference resolves in
// the enclosing scope that declares its table (#1089).
func TestExtractResolvedCorrelatedSubquery(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT o.id FROM orders o WHERE EXISTS (SELECT 1 FROM customers c WHERE c.owner = o.id)")
	want := []Column{
		{Namespace: "sales", Table: "orders", Name: "id"},
		{Namespace: "sales", Table: "customers", Name: "owner"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedJoinsThroughSources: a join through a CTE, a derived table
// or an explicitly aliased source names the physical tables it reads (#1089).
func TestExtractResolvedJoinsThroughSources(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	cases := []struct {
		name string
		sql  string
		want []Join
	}{
		{
			"cte",
			"WITH x AS (SELECT customer_id, id FROM orders) SELECT * FROM x JOIN customers c ON x.customer_id = c.id",
			[]Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"cte with an aliased source",
			"WITH x AS (SELECT o.customer_id FROM orders o) SELECT * FROM x JOIN customers c ON x.customer_id = c.id",
			[]Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}},
		},
		{
			"derived table",
			"SELECT * FROM (SELECT id FROM orders) q JOIN customers c ON q.id = c.id",
			[]Join{{LeftAlias: "orders", LeftColumn: "id", RightAlias: "c", RightColumn: "id"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractResolved(d, "sales", tc.sql).Joins; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Joins = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestExtractResolvedNestedSources: a source that reads another CTE is
// followed to its physical table (#1089).
func TestExtractResolvedNestedSources(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH y AS (SELECT id FROM orders), x AS (SELECT id FROM y) SELECT x.id FROM x")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedAmbiguousSource: a source that reads several tables is
// ambiguous, so a reference through it is dropped rather than invented.
func TestExtractResolvedAmbiguousSource(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH x AS (SELECT o.id FROM orders o JOIN items i ON o.id = i.order_id) SELECT x.id FROM x")
	want := []Column{
		{Name: "id"}, // x.id: ambiguous through a two-table CTE, no phantom table
		{Namespace: "sales", Table: "orders", Name: "id"},
		{Namespace: "sales", Table: "items", Name: "order_id"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedUnionBranches: an unqualified column resolves against the
// single table of its own branch (#1089).
func TestExtractResolvedUnionBranches(t *testing.T) {
	t.Parallel()
	got := Extract("SELECT id FROM a UNION ALL SELECT id FROM b")
	want := []Column{{Table: "a", Name: "id"}, {Table: "b", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedSelectedSourceOnly: a scope that declares a CTE definition
// but selects one of its aliases indexes only the selected source (#1089).
func TestExtractResolvedSelectedSourceOnly(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH x AS (SELECT * FROM orders) SELECT customer_id FROM x y")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "customer_id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedProjectionAlias: a join through a projected column resolves
// to the physical column behind the output name (#1089).
func TestExtractResolvedProjectionAlias(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH x AS (SELECT customer_id AS cid FROM orders) SELECT * FROM x JOIN customers c ON x.cid = c.id")
	want := []Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedLocalDeclarationStopsLookup: an ambiguous local alias must
// not borrow a table from an outer scope that declares the same alias (#1089).
func TestExtractResolvedLocalDeclarationStopsLookup(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	sql := `WITH x AS (SELECT id FROM orders o)
		SELECT * FROM x
		WHERE EXISTS (
			SELECT 1 FROM (
				SELECT c.id FROM customers c JOIN items i ON c.id = i.customer_id
			) x
			JOIN payments p ON x.id = p.customer_id
		)`
	got := ExtractResolved(d, "sales", sql).Joins
	want := []Join{{LeftAlias: "c", LeftColumn: "id", RightAlias: "i", RightColumn: "customer_id"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Joins = %#v, want %#v", got, want)
	}
}

// TestExtractResolvedDeclaredSourceStaysAmbiguous: a scope that declares two
// sources is ambiguous even when one of them cannot be resolved (#1089).
func TestExtractResolvedDeclaredSourceStaysAmbiguous(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	sql := "WITH x AS (SELECT i.order_id FROM orders o JOIN items i ON o.id = i.order_id) SELECT order_id FROM x CROSS JOIN customers c"
	got := ExtractResolved(d, "sales", sql).Columns
	want := []Column{
		{Name: "order_id"}, // two declared sources (x and c): no table guessed
		{Namespace: "sales", Table: "items", Name: "order_id"},
		{Namespace: "sales", Table: "orders", Name: "id"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Columns = %#v, want %#v", got, want)
	}
}

// TestExtractResolvedAliasFilterPerScope: a projection alias in one scope does
// not hide a same-named physical column in another (#1089).
func TestExtractResolvedAliasFilterPerScope(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT 1 AS status WHERE EXISTS (SELECT status FROM shipments)")
	want := []Column{{Namespace: "sales", Table: "shipments", Name: "status"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedSemiJoin: the right side of a SEMI or ANTI JOIN is not a
// projected source, but its ON predicate still resolves (#1089).
func TestExtractResolvedSemiJoin(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT * FROM orders o LEFT SEMI JOIN customers c ON o.customer_id = c.id")
	want := []Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedUnquotedAliasCase: an unquoted qualifier matches its
// alias regardless of case, as SQL folds unquoted identifiers (#1089).
func TestExtractResolvedUnquotedAliasCase(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT o.id FROM orders O")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedQuotedAliases: aliases that differ only in case stay
// distinct (#1089).
func TestExtractResolvedQuotedAliases(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	got := ExtractResolved(d, "sales", `SELECT "O".customer_id FROM orders AS "O" JOIN customers o ON "O".id = o.id`)
	want := []Column{
		{Namespace: "sales", Table: "orders", Name: "customer_id"},
		{Namespace: "sales", Table: "orders", Name: "id"},
		{Namespace: "sales", Table: "customers", Name: "id"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedCTEColumnList: an explicit CTE column list names its
// outputs by position (#1089).
func TestExtractResolvedCTEColumnList(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH x(b, a) AS (SELECT a, b FROM orders) SELECT * FROM x JOIN customers c ON x.a = c.id")
	want := []Join{{LeftAlias: "orders", LeftColumn: "b", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedQualifiedStar: a qualified star exposes the columns of the
// source it expands (#1089).
func TestExtractResolvedQualifiedStar(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT * FROM (SELECT o.* FROM orders o) q JOIN customers c ON q.customer_id = c.id")
	want := []Join{{LeftAlias: "o", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedStarKeepsProjection: a star over a CTE keeps the upstream
// output-to-column mappings (#1089).
func TestExtractResolvedStarKeepsProjection(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "WITH x AS (SELECT customer_id AS cid FROM orders), y AS (SELECT * FROM x) SELECT * FROM y JOIN customers c ON y.cid = c.id")
	want := []Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedQuotedProjection: projected names that differ only in case
// stay distinct (#1089).
func TestExtractResolvedQuotedProjection(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	got := ExtractResolved(d, "sales", `WITH x AS (SELECT customer_id AS "ID", id AS id FROM orders) SELECT * FROM x JOIN customers c ON x."ID" = c.id`)
	want := []Join{{LeftAlias: "orders", LeftColumn: "customer_id", RightAlias: "c", RightColumn: "id"}}
	if !reflect.DeepEqual(got.Joins, want) {
		t.Fatalf("Joins = %#v, want %#v", got.Joins, want)
	}
}

// TestExtractResolvedReturning: a RETURNING name binds to the write target,
// never to a source the write reads (#1089).
func TestExtractResolvedReturning(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	got := ExtractResolved(d, "sales", "INSERT INTO target(dst) SELECT src FROM source RETURNING dst")
	want := []Column{
		{Namespace: "sales", Table: "source", Name: "src"},
		{Namespace: "sales", Table: "target", Name: "dst"},
	}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedUnqualifiedStaysAmbiguous: an unqualified name that could
// bind to a local or a visible outer source is not guessed (#1089).
func TestExtractResolvedUnqualifiedStaysAmbiguous(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	got := ExtractResolved(d, "sales", "SELECT 1 FROM orders WHERE EXISTS (SELECT 1 FROM customers WHERE order_flag = 1)")
	want := []Column{{Name: "order_flag"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
	}
}

// TestExtractResolvedProjectionQualifier: a projection maps a column only when
// its qualifier names a source of the scope that projects it (#1089).
func TestExtractResolvedProjectionQualifier(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	got := ExtractResolved(d, "sales", "SELECT 1 FROM orders o WHERE EXISTS (WITH x AS (SELECT o.id AS k FROM items i) SELECT 1 FROM x JOIN customers c ON x.k = c.id)")
	if len(got.Joins) != 0 {
		t.Fatalf("Joins = %#v, want none", got.Joins)
	}
}

// TestExtractResolvedDerivedTableVisibility: a non-lateral derived table cannot
// see the sources of the FROM it belongs to (#1089).
func TestExtractResolvedDerivedTableVisibility(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	got := ExtractResolved(d, "sales", "SELECT 1 FROM orders o WHERE EXISTS (SELECT 1 FROM customers o, (SELECT o.id) q)")
	want := []Column{{Namespace: "sales", Table: "orders", Name: "id"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("Columns = %#v, want %#v", got.Columns, want)
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
