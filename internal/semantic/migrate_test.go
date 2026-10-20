package semantic

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func sp(s string) *string { return &s }

func TestMigrateV1Aggregation(t *testing.T) {
	in := MigrateV1Input{
		CatalogID:  "cat",
		ModelSlug:  "orders",
		PrimaryKey: []string{"id"},
		MetricSlug: "revenue",
		Source:     ModelSource{Kind: SourceTable, Namespace: []string{"sales"}, Table: "orders"},
		Metric: SimpleMetricV1{
			Type:        DefinitionType,
			Aggregation: dialect.AggSum,
			Column:      sp("amount"),
			Filters:     []Filter{{Column: "status", Op: OpEq, Values: []any{"paid"}}},
		},
		Columns: []ColumnType{
			{Name: "amount", Type: dialect.TypeInt64},
			{Name: "status", Type: dialect.TypeString},
			{Name: "created_at", Type: dialect.TypeTimestamp},
		},
	}
	model, metric, err := MigrateV1(in)
	if err != nil {
		t.Fatalf("MigrateV1: %v", err)
	}
	if model.Slug != "orders" || model.Source.Table != "orders" || len(model.PrimaryKey) != 1 {
		t.Fatalf("unexpected model: %+v", model)
	}
	if metric.Name != "revenue" || metric.Aggregation != dialect.AggSum || metric.Dimension != "amount" {
		t.Fatalf("unexpected metric: %+v", metric)
	}
	dims := map[string]Dimension{}
	for _, d := range model.Dimensions {
		dims[d.Name] = d
	}
	if dims["amount"].Type != dialect.TypeInt64 || dims["status"].Type != dialect.TypeString {
		t.Fatalf("dimensions lost their types: %+v", dims)
	}
	// Unused columns are not carried into the model.
	if _, ok := dims["created_at"]; ok {
		t.Fatalf("unused column became a dimension: %+v", dims)
	}
}

func TestMigrateV1CustomSQLLeavesColumns(t *testing.T) {
	in := MigrateV1Input{
		CatalogID:  "cat",
		ModelSlug:  "orders",
		PrimaryKey: []string{"id"},
		MetricSlug: "custom",
		Source:     ModelSource{Kind: SourceTable, Table: "orders"},
		Metric: SimpleMetricV1{
			Type:      DefinitionType,
			CustomSQL: sp("SUM(amount * 1.2)"),
		},
		Columns: []ColumnType{{Name: "amount", Type: dialect.TypeInt64}},
	}
	model, metric, err := MigrateV1(in)
	if err != nil {
		t.Fatalf("MigrateV1: %v", err)
	}
	if metric.SQL != "SUM(amount * 1.2)" || metric.Aggregation != "" {
		t.Fatalf("custom_sql not carried: %+v", metric)
	}
	if len(model.Dimensions) != 1 || model.Dimensions[0].Name != "amount" {
		t.Fatalf("custom_sql model should keep the reported columns: %+v", model.Dimensions)
	}
}

func TestMigrateV1RejectsUnknownColumnType(t *testing.T) {
	in := MigrateV1Input{
		CatalogID:  "cat",
		ModelSlug:  "orders",
		PrimaryKey: []string{"id"},
		MetricSlug: "revenue",
		Source:     ModelSource{Kind: SourceTable, Table: "orders"},
		Metric: SimpleMetricV1{
			Type:        DefinitionType,
			Aggregation: dialect.AggSum,
			Column:      sp("amount"),
		},
	}
	if _, _, err := MigrateV1(in); !errors.Is(err, ErrInvalidExplore) {
		t.Fatalf("MigrateV1 = %v, want ErrInvalidExplore", err)
	}
}
