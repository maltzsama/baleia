package semantic

import (
	"fmt"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
)

// ColumnType is one table column with the logical type the metadata layer
// reported. A v1 metric does not store column types, so the v1→v2 migration
// needs them from the catalog metadata.
type ColumnType struct {
	Name string
	Type dialect.LogicalType
}

// MigrateV1Input describes one v1 metric and the table facts the migration
// needs.
type MigrateV1Input struct {
	CatalogID  string // Baleia catalog UUID
	BindingID  string // optional binding UUID
	ModelSlug  string
	Label      string
	Source     ModelSource // SourceTable; Namespace and Table are required
	PrimaryKey []string
	MetricSlug string
	Metric     SimpleMetricV1
	Columns    []ColumnType
}

// MigrateV1 converts a v1 simple metric into a v2 model + metric. The metric
// keeps its slug and semantics; custom_sql becomes a dialect-bound model metric.
// The caller de-duplicates ModelSlug across the workspace (v1 metrics of the
// same table share one model).
func MigrateV1(in MigrateV1Input) (Model, Metric, error) {
	if in.ModelSlug == "" || in.MetricSlug == "" {
		return Model{}, Metric{}, fmt.Errorf("%w: model and metric slugs are required", ErrInvalidExplore)
	}
	if in.Source.Kind != SourceTable || in.Source.Table == "" {
		return Model{}, Metric{}, fmt.Errorf("%w: v1 migration needs a table source", ErrInvalidExplore)
	}
	if len(in.PrimaryKey) == 0 {
		return Model{}, Metric{}, fmt.Errorf("%w: v1 migration needs a primary key", ErrInvalidExplore)
	}
	types := make(map[string]dialect.LogicalType, len(in.Columns))
	for _, c := range in.Columns {
		types[c.Name] = c.Type
	}

	var used []string
	add := func(name string) {
		if name == "" || slices.Contains(used, name) {
			return
		}
		used = append(used, name)
	}
	if in.Metric.CustomSQL != nil {
		// The escape hatch hides its columns; carry every column the caller
		// reported so the model stays usable.
		for _, c := range in.Columns {
			add(c.Name)
		}
	} else {
		if in.Metric.Column != nil {
			add(*in.Metric.Column)
		}
		if in.Metric.TimeColumn != nil {
			add(*in.Metric.TimeColumn)
		}
	}
	for _, f := range in.Metric.Filters {
		add(f.Column)
	}

	dims := make([]Dimension, 0, len(used))
	for _, name := range used {
		t, ok := types[name]
		if !ok {
			return Model{}, Metric{}, fmt.Errorf("%w: column %q has no metadata type", ErrInvalidExplore, name)
		}
		dim := Dimension{Name: name, Type: t}
		if in.Metric.TimeColumn != nil && name == *in.Metric.TimeColumn {
			dim.Grains = slices.Clone(in.Metric.TimeGrains)
		}
		dims = append(dims, dim)
	}

	out := Metric{Name: in.MetricSlug, Format: in.Metric.Format}
	if in.Metric.CustomSQL != nil {
		out.SQL = strings.TrimSpace(*in.Metric.CustomSQL)
	} else {
		out.Aggregation = in.Metric.Aggregation
		if in.Metric.Column != nil {
			out.Dimension = *in.Metric.Column
		}
		out.Filters = in.Metric.Filters
	}

	label := in.Label
	if label == "" {
		label = in.Source.Table
	}
	model := Model{
		Slug:       in.ModelSlug,
		Label:      label,
		CatalogID:  in.CatalogID,
		BindingID:  in.BindingID,
		Source:     in.Source,
		PrimaryKey: slices.Clone(in.PrimaryKey),
		Dimensions: dims,
		Metrics:    []Metric{out},
	}
	if err := model.Validate(nil); err != nil {
		return Model{}, Metric{}, err
	}
	return model, out, nil
}
