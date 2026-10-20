package semantic

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func ordersModel() Model {
	return Model{
		Slug:       "orders",
		CatalogID:  "cat",
		BindingID:  "bind",
		PrimaryKey: []string{"order_id"},
		Source:     ModelSource{Kind: SourceTable, Namespace: []string{"sales"}, Table: "orders"},
		Dimensions: []Dimension{
			{Name: "order_id", Type: dialect.TypeInt64},
			{Name: "created_at", Type: dialect.TypeTimestamp, Grains: []dialect.TimeGrain{dialect.GrainDay, dialect.GrainMonth}},
			{Name: "status", Type: dialect.TypeString},
		},
		Metrics: []Metric{
			{Name: "revenue", Aggregation: dialect.AggSum, Dimension: "order_id"},
			{Name: "orders", Aggregation: dialect.AggCount},
			{Name: "aov", Derived: &DerivedMetric{Left: "revenue", Op: "/", Right: "orders"}},
		},
		Joins: []Join{{
			To:           "customers",
			Relationship: ManyToOne,
			On:           []JoinKey{{From: "customer_id", To: "customer_id"}},
		}},
	}
}

func customersModel() Model {
	return Model{
		Slug:       "customers",
		CatalogID:  "cat",
		BindingID:  "bind",
		PrimaryKey: []string{"customer_id"},
		Source:     ModelSource{Kind: SourceTable, Namespace: []string{"sales"}, Table: "customers"},
		Dimensions: []Dimension{
			{Name: "customer_id", Type: dialect.TypeInt64},
			{Name: "segment", Type: dialect.TypeString},
		},
	}
}

func testModels() map[string]Model {
	return map[string]Model{"orders": ordersModel(), "customers": customersModel()}
}

func TestModelValidate(t *testing.T) {
	m := testModels()
	if err := m["orders"].Validate(m); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Model)
	}{
		{"missing slug", func(m *Model) { m.Slug = "" }},
		{"unknown source", func(m *Model) { m.Source.Kind = "view" }},
		{"table without table", func(m *Model) { m.Source.Table = "" }},
		{"no primary key", func(m *Model) { m.PrimaryKey = nil }},
		{"duplicate dimension", func(m *Model) { m.Dimensions = append(m.Dimensions, m.Dimensions[0]) }},
		{"unknown dimension type", func(m *Model) { m.Dimensions[0].Type = "blob" }},
		{"grains on non-time", func(m *Model) { m.Dimensions[0].Grains = []dialect.TimeGrain{dialect.GrainDay} }},
		{"metric references unknown dimension", func(m *Model) { m.Metrics[0].Dimension = "nope" }},
		{"aggregation without dimension", func(m *Model) { m.Metrics[0].Dimension = "" }},
		{"derived with bad op", func(m *Model) { m.Metrics[2].Derived.Op = "%" }},
		{"derived also aggregates", func(m *Model) { m.Metrics[2].Aggregation = dialect.AggSum }},
		{"join unknown target", func(m *Model) { m.Joins[0].To = "missing" }},
		{"join bad relationship", func(m *Model) { m.Joins[0].Relationship = "sibling" }},
		{"join no keys", func(m *Model) { m.Joins[0].On = nil }},
	}
	for _, tc := range cases {
		m := testModels()
		orders := m["orders"]
		tc.mutate(&orders)
		if err := orders.Validate(m); !errors.Is(err, ErrInvalidExplore) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidExplore", tc.name, err)
		}
	}
}

func TestExploreQueryValidate(t *testing.T) {
	models := testModels()
	valid := ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"status", "segment"},
		Metrics:    []string{"revenue", "aov"},
		Filters:    []ExploreFilter{{Field: "status", Op: OpEq, Values: []any{"paid"}}},
		Sorts:      []Sort{{Field: "revenue", Desc: true}},
		Limit:      100,
	}
	if err := valid.Validate(models); err != nil {
		t.Fatalf("valid explore query rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ExploreQuery)
	}{
		{"unknown model", func(q *ExploreQuery) { q.Model = "nope" }},
		{"nothing selected", func(q *ExploreQuery) { q.Dimensions = nil; q.Metrics = nil }},
		{"unknown dimension", func(q *ExploreQuery) { q.Dimensions = []string{"nope"} }},
		{"unknown metric", func(q *ExploreQuery) { q.Metrics = []string{"nope"} }},
		{"dimension used as metric", func(q *ExploreQuery) { q.Metrics = []string{"status"} }},
		{"duplicate dimension", func(q *ExploreQuery) { q.Dimensions = []string{"status", "status"} }},
		{"filter unknown field", func(q *ExploreQuery) { q.Filters[0].Field = "nope" }},
		{"filter without values", func(q *ExploreQuery) { q.Filters[0].Values = nil }},
		{"sort unknown field", func(q *ExploreQuery) { q.Sorts[0].Field = "nope" }},
		{"negative limit", func(q *ExploreQuery) { q.Limit = -1 }},
		{"limit too high", func(q *ExploreQuery) { q.Limit = MaxExploreLimit + 1 }},
		{"time grain mismatch", func(q *ExploreQuery) { q.TimeGrain = "hour" }},
	}
	for _, tc := range cases {
		q := valid
		tc.mutate(&q)
		if err := q.Validate(models); !errors.Is(err, ErrInvalidExplore) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidExplore", tc.name, err)
		}
	}
}

func TestExploreShareRoundTrip(t *testing.T) {
	q := ExploreQuery{
		Model:      "orders",
		Dimensions: []string{"created_at"},
		Metrics:    []string{"revenue"},
		TimeGrain:  "day",
		Limit:      50,
	}
	enc, err := q.EncodeShare()
	if err != nil {
		t.Fatalf("EncodeShare: %v", err)
	}
	got, err := ParseShare(enc)
	if err != nil {
		t.Fatalf("ParseShare: %v", err)
	}
	if got.Model != q.Model || got.TimeGrain != q.TimeGrain || got.Limit != q.Limit || len(got.Metrics) != 1 {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	if _, err := ParseShare("not-base64!"); !errors.Is(err, ErrInvalidExplore) {
		t.Errorf("ParseShare accepted garbage: %v", err)
	}
}
