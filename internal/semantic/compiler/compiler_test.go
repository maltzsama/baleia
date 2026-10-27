package compiler

import (
	"errors"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/semantic"
)

func models() map[string]semantic.Model {
	orders := semantic.Model{
		Slug:       "orders",
		CatalogID:  "cat",
		BindingID:  "bind",
		PrimaryKey: []string{"order_id"},
		Source:     semantic.ModelSource{Kind: semantic.SourceTable, Namespace: []string{"sales"}, Table: "orders"},
		Dimensions: []semantic.Dimension{
			{Name: "order_id", Type: dialect.TypeInt64},
			{Name: "created_at", Type: dialect.TypeTimestamp, Grains: []dialect.TimeGrain{dialect.GrainDay, dialect.GrainMonth}},
			{Name: "status", Type: dialect.TypeString},
		},
		Metrics: []semantic.Metric{
			{Name: "revenue", Aggregation: dialect.AggSum, Dimension: "order_id"},
			{Name: "orders", Aggregation: dialect.AggCount},
			{Name: "aov", Derived: &semantic.DerivedMetric{Left: "revenue", Op: "/", Right: "orders"}},
		},
		Joins: []semantic.Join{{
			To:           "customers",
			Relationship: semantic.ManyToOne,
			On:           []semantic.JoinKey{{From: "customer_id", To: "customer_id"}},
		}},
	}
	customers := semantic.Model{
		Slug:       "customers",
		CatalogID:  "cat",
		BindingID:  "bind",
		PrimaryKey: []string{"customer_id"},
		Source:     semantic.ModelSource{Kind: semantic.SourceTable, Namespace: []string{"sales"}, Table: "customers"},
		Dimensions: []semantic.Dimension{
			{Name: "customer_id", Type: dialect.TypeInt64},
			{Name: "segment", Type: dialect.TypeString},
		},
		Metrics: []semantic.Metric{
			{Name: "customer_count", Aggregation: dialect.AggCount},
		},
	}
	return map[string]semantic.Model{"orders": orders, "customers": customers}
}

func pg(t *testing.T) dialect.Dialect {
	t.Helper()
	d, ok := dialect.NewRegistry().Get(dialect.PostgreSQL)
	if !ok {
		t.Fatal("postgresql dialect missing")
	}
	return d
}

func TestCompileSingleModel(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status"},
		Metrics:    []string{"revenue"},
		Filters:    []semantic.ExploreFilter{{Field: "status", Op: semantic.OpEq, Values: []any{"paid"}}},
		Sorts:      []semantic.Sort{{Field: "revenue", Desc: true}},
		Limit:      10,
	}
	got, err := Compile(q, models(), pg(t), Options{Catalog: "lake"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, want := range []string{
		`SELECT "status" AS "status", SUM("order_id") AS "revenue"`,
		`FROM "sales"."orders" "orders"`,
		`WHERE "status" = 'paid'`,
		`GROUP BY "status"`,
		`ORDER BY 2 DESC`,
		`LIMIT 10`,
	} {
		if !strings.Contains(got.SQL, want) {
			t.Errorf("SQL missing %q:\n%s", want, got.SQL)
		}
	}
}

func TestCompileTimeGrain(t *testing.T) {
	t.Parallel()
	d := pg(t)
	trunc, err := d.TimeTrunc(`"created_at"`, dialect.GrainDay)
	if err != nil {
		t.Fatal(err)
	}
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"created_at"},
		Metrics:    []string{"orders"},
		TimeGrain:  "day",
	}
	got, err := Compile(q, models(), d, Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(got.SQL, trunc+` AS "created_at"`) {
		t.Errorf("SQL missing truncation %q:\n%s", trunc, got.SQL)
	}
	if !strings.Contains(got.SQL, `COUNT(*) AS "orders"`) {
		t.Errorf("SQL missing count:\n%s", got.SQL)
	}
}

func TestCompileMetricFilterUsesHaving(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:   "orders",
		Metrics: []string{"revenue"},
		Filters: []semantic.ExploreFilter{{Field: "revenue", Op: semantic.OpGt, Values: []any{100}}},
	}
	got, err := Compile(q, models(), pg(t), Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(got.SQL, `HAVING SUM("order_id") > 100`) {
		t.Errorf("SQL missing HAVING:\n%s", got.SQL)
	}
}

func TestCompileJoinDimension(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status", "segment"},
		Metrics:    []string{"revenue"},
		Sorts:      []semantic.Sort{{Field: "segment"}},
		Limit:      20,
	}
	got, err := Compile(q, models(), pg(t), Options{Catalog: "lake"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, want := range []string{
		`SELECT "orders"."status" AS "status", "customers"."segment" AS "segment", SUM("orders"."order_id") AS "revenue"`,
		`FROM "sales"."orders" "orders" LEFT JOIN "sales"."customers" "customers" ON "orders"."customer_id" = "customers"."customer_id"`,
		`GROUP BY "orders"."status", "customers"."segment"`,
		`ORDER BY 2`,
		`LIMIT 20`,
	} {
		if !strings.Contains(got.SQL, want) {
			t.Errorf("SQL missing %q:\n%s", want, got.SQL)
		}
	}
}

func TestCompileJoinFilterOnJoinedDimension(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"segment"},
		Metrics:    []string{"orders"},
		Filters:    []semantic.ExploreFilter{{Field: "segment", Op: semantic.OpEq, Values: []any{"enterprise"}}},
	}
	got, err := Compile(q, models(), pg(t), Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(got.SQL, `WHERE "customers"."segment" = 'enterprise'`) {
		t.Errorf("SQL missing the joined filter:\n%s", got.SQL)
	}
}

func TestCompileRejectsJoinedMetrics(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"segment"}, Metrics: []string{"customer_count"}}
	_, err := Compile(q, models(), pg(t), Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}

func TestCompileRejectsOneToManyJoin(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.Joins[0].Relationship = semantic.OneToMany
	m["orders"] = orders
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"segment"}, Metrics: []string{"orders"}}
	_, err := Compile(q, m, pg(t), Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}

func TestCompileRejectsChainedJoin(t *testing.T) {
	t.Parallel()
	m := models()
	customers := m["customers"]
	customers.Joins = []semantic.Join{{To: "regions", Relationship: semantic.ManyToOne, On: []semantic.JoinKey{{From: "region_id", To: "region_id"}}}}
	m["customers"] = customers
	m["regions"] = semantic.Model{
		Slug: "regions", CatalogID: "cat", BindingID: "bind", PrimaryKey: []string{"region_id"},
		Source:     semantic.ModelSource{Kind: semantic.SourceTable, Table: "regions"},
		Dimensions: []semantic.Dimension{{Name: "region_id", Type: dialect.TypeInt64}, {Name: "region", Type: dialect.TypeString}},
	}
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"region"}, Metrics: []string{"orders"}}
	// Chained fields are not reachable from the base yet: validation rejects
	// them instead of compiling SQL against an ambiguous join tree.
	if _, err := Compile(q, m, pg(t), Options{}); !errors.Is(err, semantic.ErrInvalidExplore) {
		t.Fatalf("Compile = %v, want ErrInvalidExplore", err)
	}
}

func TestCompileDerivedMetric(t *testing.T) {
	t.Parallel()
	d := pg(t)
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"status"}, Metrics: []string{"aov"}}
	got, err := Compile(q, models(), d, Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	wantOuter := d.SafeDivide(`"revenue"`, `"orders"`) + ` AS "aov"`
	for _, want := range []string{
		`SUM("order_id") AS "revenue"`,
		`COUNT(*) AS "orders"`,
		`baleia_explore`,
		wantOuter,
	} {
		if !strings.Contains(got.SQL, want) {
			t.Errorf("SQL missing %q:\n%s", want, got.SQL)
		}
	}
}

func TestCompileDerivedMetricFilter(t *testing.T) {
	t.Parallel()
	d := pg(t)
	q := semantic.ExploreQuery{
		Model:   "orders",
		Metrics: []string{"revenue"},
		Filters: []semantic.ExploreFilter{{Field: "aov", Op: semantic.OpGt, Values: []any{10}}},
	}
	got, err := Compile(q, models(), d, Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := "WHERE " + d.SafeDivide(`"revenue"`, `"orders"`) + " > 10"
	if !strings.Contains(got.SQL, want) {
		t.Errorf("SQL missing derived filter %q:\n%s", want, got.SQL)
	}
}

func TestCompileRejectsNestedDerived(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.Metrics = append(orders.Metrics, semantic.Metric{
		Name: "aov2", Derived: &semantic.DerivedMetric{Left: "aov", Op: "*", Right: "revenue"},
	})
	m["orders"] = orders
	q := semantic.ExploreQuery{Model: "orders", Metrics: []string{"aov2"}}
	if _, err := Compile(q, m, pg(t), Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}

func TestCompileRejectsUnselectedSort(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status"},
		Metrics:    []string{"orders"},
		Sorts:      []semantic.Sort{{Field: "created_at"}},
	}
	_, err := Compile(q, models(), pg(t), Options{})
	if !errors.Is(err, semantic.ErrInvalidExplore) {
		t.Fatalf("Compile = %v, want ErrInvalidExplore", err)
	}
}

// TestCompileEveryDialect is the conformance gate: the same query renders for
// every registered dialect without a per-dialect special case.
func TestCompileEveryDialect(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status", "segment"},
		Metrics:    []string{"revenue", "orders", "aov"},
		Filters:    []semantic.ExploreFilter{{Field: "status", Op: semantic.OpEq, Values: []any{"paid"}}},
		Sorts:      []semantic.Sort{{Field: "revenue", Desc: true}},
		Limit:      25,
	}
	for _, name := range dialect.NewRegistry().Names() {
		d, ok := dialect.NewRegistry().Get(name)
		if !ok {
			continue
		}
		if _, err := Compile(q, models(), d, Options{Catalog: "lake"}); err != nil {
			t.Errorf("%s: Compile: %v", name, err)
		}
	}
}

func TestCompileMetricFiltersAreConditionalAggregates(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.Metrics[0].Filters = []semantic.Filter{{Column: "status", Op: semantic.OpEq, Values: []any{"paid"}}}
	m["orders"] = orders
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"status"}, Metrics: []string{"revenue"}}
	got, err := Compile(q, m, pg(t), Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `SUM(CASE WHEN "status" = 'paid' THEN "order_id" END) AS "revenue"`
	if !strings.Contains(got.SQL, want) {
		t.Errorf("SQL missing conditional aggregate %q:\n%s", want, got.SQL)
	}
}

func TestCompileMigratedV1MetricWithFilter(t *testing.T) {
	t.Parallel()
	model, _, err := semantic.MigrateV1(semantic.MigrateV1Input{
		CatalogID:  "cat",
		ModelSlug:  "orders",
		PrimaryKey: []string{"id"},
		MetricSlug: "revenue",
		Source:     semantic.ModelSource{Kind: semantic.SourceTable, Namespace: []string{"sales"}, Table: "orders"},
		Metric: semantic.SimpleMetricV1{
			Type:        semantic.DefinitionType,
			Aggregation: dialect.AggSum,
			Column:      strPtr("amount"),
			Filters:     []semantic.Filter{{Column: "status", Op: semantic.OpEq, Values: []any{"paid"}}},
		},
		Columns: []semantic.ColumnType{
			{Name: "amount", Type: dialect.TypeInt64},
			{Name: "status", Type: dialect.TypeString},
		},
	})
	if err != nil {
		t.Fatalf("MigrateV1: %v", err)
	}
	got, err := Compile(semantic.ExploreQuery{Model: "orders", Metrics: []string{"revenue"}}, map[string]semantic.Model{model.Slug: model}, pg(t), Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(got.SQL, `SUM(CASE WHEN "status" = 'paid' THEN "amount" END) AS "revenue"`) {
		t.Errorf("migrated filter lost:\n%s", got.SQL)
	}
}

func TestCompilePortableGroupBy(t *testing.T) {
	t.Parallel()
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"status"}, Metrics: []string{"orders"}}
	for _, name := range []string{dialect.TSQL, dialect.Oracle, dialect.PostgreSQL} {
		d, ok := dialect.NewRegistry().Get(name)
		if !ok {
			continue
		}
		got, err := Compile(q, models(), d, Options{})
		if err != nil {
			t.Fatalf("%s: Compile: %v", name, err)
		}
		if strings.Contains(strings.ToUpper(got.SQL), "GROUP BY 1") {
			t.Errorf("%s: GROUP BY still positional:\n%s", name, got.SQL)
		}
	}
}

func strPtr(s string) *string { return &s }

func TestCompileCalculatedField(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.CalculatedFields = []semantic.CalculatedField{{Name: "margin", SQL: `"revenue" / 2`}}
	m["orders"] = orders
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"status"}, Metrics: []string{"margin"}}
	got, err := Compile(q, m, pg(t), Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(got.SQL, `"revenue" / 2 AS "margin"`) {
		t.Errorf("calculated field not rendered in the outer query:\n%s", got.SQL)
	}
	if !strings.Contains(got.SQL, `SUM("order_id") AS "revenue"`) {
		t.Errorf("base metrics needed by the calculated field are missing:\n%s", got.SQL)
	}
}

func TestCompileOneToManyJoinedMetric(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.Joins = append(orders.Joins, semantic.Join{
		To:           "order_items",
		Relationship: semantic.OneToMany,
		On:           []semantic.JoinKey{{From: "order_id", To: "order_id"}},
	})
	m["orders"] = orders
	m["order_items"] = semantic.Model{
		Slug: "order_items", CatalogID: "cat", BindingID: "bind", PrimaryKey: []string{"id"},
		Source:     semantic.ModelSource{Kind: semantic.SourceTable, Namespace: []string{"sales"}, Table: "order_items"},
		Dimensions: []semantic.Dimension{{Name: "quantity", Type: dialect.TypeInt64}},
		Metrics:    []semantic.Metric{{Name: "quantity_sum", Aggregation: dialect.AggSum, Dimension: "quantity"}},
	}
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"status"}, Metrics: []string{"quantity_sum"}}
	got, err := Compile(q, m, pg(t), Options{Catalog: "lake"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, want := range []string{
		`SELECT "orders"."status" AS "status", SUM("order_items"."quantity") AS "quantity_sum"`,
		`LEFT JOIN "sales"."order_items" "order_items" ON "orders"."order_id" = "order_items"."order_id"`,
		`GROUP BY "orders"."status"`,
	} {
		if !strings.Contains(got.SQL, want) {
			t.Errorf("SQL missing %q:\n%s", want, got.SQL)
		}
	}
}

func TestCompileRejectsBaseMetricWithOneToMany(t *testing.T) {
	t.Parallel()
	m := models()
	orders := m["orders"]
	orders.Joins[0].Relationship = semantic.OneToMany
	m["orders"] = orders
	q := semantic.ExploreQuery{Model: "orders", Dimensions: []string{"segment"}, Metrics: []string{"orders"}}
	if _, err := Compile(q, m, pg(t), Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}
