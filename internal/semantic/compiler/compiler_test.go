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
	}
	return map[string]semantic.Model{"orders": orders, "customers": customers}
}

func pg(t *testing.T) dialect.Dialect {
	t.Helper()
	d, ok := dialect.Get(dialect.PostgreSQL)
	if !ok {
		t.Fatal("postgresql dialect missing")
	}
	return d
}

func TestCompileSingleModel(t *testing.T) {
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
		`FROM "sales"."orders" AS "orders"`,
		`WHERE "status" = 'paid'`,
		`GROUP BY 1`,
		`ORDER BY 2 DESC`,
		`LIMIT 10`,
	} {
		if !strings.Contains(got.SQL, want) {
			t.Errorf("SQL missing %q:\n%s", want, got.SQL)
		}
	}
}

func TestCompileTimeGrain(t *testing.T) {
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

func TestCompileRejectsJoinedFields(t *testing.T) {
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"segment"},
		Metrics:    []string{"revenue"},
	}
	_, err := Compile(q, models(), pg(t), Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}

func TestCompileRejectsDerivedMetrics(t *testing.T) {
	q := semantic.ExploreQuery{Model: "orders", Metrics: []string{"aov"}}
	_, err := Compile(q, models(), pg(t), Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Compile = %v, want ErrUnsupported", err)
	}
}

func TestCompileRejectsUnselectedSort(t *testing.T) {
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
	q := semantic.ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status"},
		Metrics:    []string{"revenue", "orders"},
		Filters:    []semantic.ExploreFilter{{Field: "status", Op: semantic.OpEq, Values: []any{"paid"}}},
		Sorts:      []semantic.Sort{{Field: "revenue", Desc: true}},
		Limit:      25,
	}
	for _, name := range dialect.Names() {
		d, ok := dialect.Get(name)
		if !ok {
			continue
		}
		if _, err := Compile(q, models(), d, Options{Catalog: "lake"}); err != nil {
			t.Errorf("%s: Compile: %v", name, err)
		}
	}
}
