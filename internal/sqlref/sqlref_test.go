package sqlref

import (
	"reflect"
	"testing"
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
