package semantic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
)

func metric(t *testing.T, m SimpleMetricV1) SimpleMetricV1 {
	t.Helper()
	if err := m.Validate(); err != nil {
		t.Fatalf("definition should validate: %v", err)
	}
	return m
}

func TestValidate(t *testing.T) {
	t.Parallel()
	col := "total_price"
	tests := []struct {
		name string
		m    SimpleMetricV1
		want string // substring of the error; "" = valid
	}{
		{"ok", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col}, ""},
		{"count_star", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount}, ""},
		{"bad_type", SimpleMetricV1{Type: "model", Aggregation: dialect.AggSum, Column: &col}, "type must be"},
		{"bad_aggregation", SimpleMetricV1{Type: DefinitionType, Aggregation: "median", Column: &col}, "aggregation"},
		{"sum_without_column", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggSum}, "column is required"},
		{"empty_filter_column", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, Filters: []Filter{{Column: "", Op: OpEq, Values: []any{1}}}}, "column is empty"},
		{"bad_op", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, Filters: []Filter{{Column: "x", Op: "~", Values: []any{1}}}}, "op"},
		{"in_without_values", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, Filters: []Filter{{Column: "x", Op: OpIn}}}, "needs values"},
		{"is_null_with_values", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, Filters: []Filter{{Column: "x", Op: OpIsNull, Values: []any{1}}}}, "takes no values"},
		{"eq_with_two_values", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, Filters: []Filter{{Column: "x", Op: OpEq, Values: []any{1, 2}}}}, "exactly one value"},
		{"bad_grain", SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount, TimeGrains: []dialect.TimeGrain{"fortnight"}}, "time grain"},
		{"custom_sql_separator", SimpleMetricV1{Type: DefinitionType, CustomSQL: ptr("sum(x); drop table t")}, "single expression"},
		{"custom_sql_empty", SimpleMetricV1{Type: DefinitionType, CustomSQL: ptr("  ")}, "empty"},
		{"custom_sql_ok", SimpleMetricV1{Type: DefinitionType, CustomSQL: ptr("sum(x)")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.m.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"type":"simple","aggregation":"sum","column":"x","surprise":true}`)
	if _, err := ParseSimpleMetricV1(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestRenderTrino(t *testing.T) {
	t.Parallel()
	col, tc := "total_price", "order_date"
	m := metric(t, SimpleMetricV1{
		Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col,
		TimeColumn: &tc,
		Filters: []Filter{
			{Column: "status", Op: OpIn, Values: []any{"paid", "shipped"}},
			{Column: "region", Op: OpNeq, Values: []any{"test"}},
		},
	})
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{
		Alias: "net_revenue", Grain: dialect.GrainMonth, From: &from,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT date_trunc('month', "order_date") AS "period", SUM("total_price") AS "net_revenue" FROM "lake"."sales"."orders" WHERE "status" IN ('paid', 'shipped') AND "region" != 'test' AND "order_date" >= TIMESTAMP '2026-01-01 00:00:00 UTC' GROUP BY 1 ORDER BY 1`
	if got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderScalarAndLimit(t *testing.T) {
	t.Parallel()
	col := "amount"
	m := metric(t, SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggAvg, Column: &col})
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{
		Alias: "avg_amount", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT AVG("amount") AS "avg_amount" FROM "lake"."sales"."orders"`
	if !strings.Contains(got, want) {
		t.Fatalf("Render() =\n%s\nwant it to contain\n%s", got, want)
	}
	if !strings.Contains(got, "LIMIT 10") {
		t.Fatalf("Render() =\n%s\nwant a LIMIT 10 wrapper", got)
	}
}

func TestRenderCountStar(t *testing.T) {
	t.Parallel()
	m := metric(t, SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggCount})
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{Alias: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `COUNT(*) AS "orders"`) {
		t.Fatalf("Render() = %s, want COUNT(*) AS \"orders\"", got)
	}
}

func TestRenderFilterValuesAreLiterals(t *testing.T) {
	t.Parallel()
	col := "amount"
	m := metric(t, SimpleMetricV1{
		Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col,
		Filters: []Filter{{Column: "name", Op: OpEq, Values: []any{`x' OR '1'='1`}}},
	})
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{Alias: "sum_amount"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `'x'' OR ''1''=''1'`) {
		t.Fatalf("filter value was not escaped as a literal: %s", got)
	}
}

func TestRenderQuotesIdentifiers(t *testing.T) {
	t.Parallel()
	col := "amount"
	m := metric(t, SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col})
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: `we"ird`}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{Alias: "sum_amount"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"we""ird"`) {
		t.Fatalf("table name was not quoted: %s", got)
	}
}

func TestRenderGrainRequiresTimeColumn(t *testing.T) {
	t.Parallel()
	col := "amount"
	m := metric(t, SimpleMetricV1{Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col})
	_, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{Alias: "x", Grain: dialect.GrainDay})
	if err == nil || !strings.Contains(err.Error(), "without a time column") {
		t.Fatalf("expected a time-column error, got %v", err)
	}
}

func TestRenderCustomSQL(t *testing.T) {
	t.Parallel()
	m := metric(t, SimpleMetricV1{Type: DefinitionType, CustomSQL: ptr(`sum("amount") filter (where "paid")`)})
	got, err := Render(m, TableRef{Catalog: "lake", Schema: []string{"sales"}, Table: "orders"}, dialect.NewRegistry().MustGet(dialect.Trino), RenderOptions{Alias: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `sum("amount") filter (where "paid") AS "paid"`) {
		t.Fatalf("custom_sql not used as the aggregate expression: %s", got)
	}
}

func TestParseRoundTrip(t *testing.T) {
	t.Parallel()
	col := "amount"
	in := SimpleMetricV1{
		Type: DefinitionType, Aggregation: dialect.AggSum, Column: &col,
		Format: &Format{Type: "number", Decimals: 2},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseSimpleMetricV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Aggregation != in.Aggregation || out.Column == nil || *out.Column != *in.Column {
		t.Fatalf("round trip changed the definition: %+v", out)
	}
}

func ptr[T any](v T) *T { return &v }
