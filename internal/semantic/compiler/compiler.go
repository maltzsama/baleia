// Package compiler turns an explore query into SQL for one binding's dialect
// (#91). It never accepts raw SQL from the caller: the model carries SQL
// expressions, and every query reaches the engine only through the explore
// query shape.
//
// This first slice compiles a single model (the base) and refuses queries that
// need joins or derived metrics with ErrUnsupported; those land in the
// following commits of #91.
package compiler

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/semantic"
)

// ErrUnsupported means the explore query is valid but uses a feature this
// compiler slice does not render yet (joins, derived metrics). It is not a
// dialect limitation.
var ErrUnsupported = errors.New("explore query not supported yet")

// Options carry binding context the model does not store.
type Options struct {
	// Catalog is the binding's engine catalog name ("" = engine default).
	Catalog string
}

// Result is the compiled statement plus compiler notes the SQL tab shows.
type Result struct {
	SQL   string
	Notes []string
}

// Compile validates q against models and renders it for d.
func Compile(q semantic.ExploreQuery, models map[string]semantic.Model, d dialect.Dialect, opts Options) (Result, error) {
	if d == nil {
		return Result{}, fmt.Errorf("%w: no dialect", semantic.ErrInvalidExplore)
	}
	if err := q.Validate(models); err != nil {
		return Result{}, err
	}
	base := models[q.Model]
	owners, err := fieldOwners(base, models)
	if err != nil {
		return Result{}, err
	}
	for _, name := range append(slices.Clone(q.Dimensions), q.Metrics...) {
		if owners[name] != base.Slug {
			return Result{}, fmt.Errorf("%w: field %q lives on joined model %q", ErrUnsupported, name, owners[name])
		}
	}
	for _, f := range q.Filters {
		if owners[f.Field] != base.Slug {
			return Result{}, fmt.Errorf("%w: filter on joined field %q", ErrUnsupported, f.Field)
		}
	}
	for _, name := range q.Metrics {
		if metricOf(base, name).Derived != nil {
			return Result{}, fmt.Errorf("%w: derived metric %q", ErrUnsupported, name)
		}
	}
	for _, f := range q.Filters {
		if mt := metricOf(base, f.Field); mt != nil && mt.Derived != nil {
			return Result{}, fmt.Errorf("%w: filter on derived metric %q", ErrUnsupported, f.Field)
		}
	}

	selects := make([]string, 0, len(q.Dimensions)+len(q.Metrics))
	groupBy := make([]string, 0, len(q.Dimensions))
	outputPos := map[string]int{}

	for _, name := range q.Dimensions {
		dim := dimensionOf(base, name)
		expr, err := dimensionExpr(d, *dim, q.TimeGrain)
		if err != nil {
			return Result{}, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return Result{}, err
		}
		selects = append(selects, expr+" AS "+alias)
		outputPos[name] = len(selects)
		groupBy = append(groupBy, fmt.Sprintf("%d", len(selects)))
	}

	for _, name := range q.Metrics {
		expr, err := metricExpr(d, *metricOf(base, name), base.Dimensions)
		if err != nil {
			return Result{}, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return Result{}, err
		}
		selects = append(selects, expr+" AS "+alias)
		outputPos[name] = len(selects)
	}

	from, err := sourceExpr(d, base, opts)
	if err != nil {
		return Result{}, err
	}

	var where []string
	for _, f := range q.Filters {
		if metricOf(base, f.Field) != nil {
			continue // metric filters are HAVING
		}
		expr, err := dimensionExpr(d, *dimensionOf(base, f.Field), "")
		if err != nil {
			return Result{}, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return Result{}, err
		}
		where = append(where, cond)
	}

	var having []string
	for _, f := range q.Filters {
		mt := metricOf(base, f.Field)
		if mt == nil {
			continue
		}
		expr, err := metricExpr(d, *mt, base.Dimensions)
		if err != nil {
			return Result{}, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return Result{}, err
		}
		having = append(having, cond)
	}

	var order []string
	for _, s := range q.Sorts {
		pos, ok := outputPos[s.Field]
		if !ok {
			return Result{}, fmt.Errorf("%w: sort field %q is not selected", semantic.ErrInvalidExplore, s.Field)
		}
		dir := ""
		if s.Desc {
			dir = " DESC"
		}
		order = append(order, fmt.Sprintf("%d%s", pos, dir))
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(selects, ", "))
	b.WriteString(" FROM ")
	b.WriteString(from)
	if len(where) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}
	if len(groupBy) > 0 {
		b.WriteString(" GROUP BY ")
		b.WriteString(strings.Join(groupBy, ", "))
	}
	if len(having) > 0 {
		b.WriteString(" HAVING ")
		b.WriteString(strings.Join(having, " AND "))
	}
	if len(order) > 0 {
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(order, ", "))
	}
	sql := b.String()
	if q.Limit > 0 {
		sql = d.Limit(sql, q.Limit)
	}

	notes := []string{fmt.Sprintf("Compiled against %s", d.Name())}
	return Result{SQL: sql, Notes: notes}, nil
}

// dimensionExpr renders a dimension, truncating time dimensions when a grain
// is requested.
func dimensionExpr(d dialect.Dialect, dim semantic.Dimension, grain string) (string, error) {
	var expr string
	if strings.TrimSpace(dim.SQL) != "" {
		expr = strings.TrimSpace(dim.SQL)
	} else {
		q, err := d.QuoteIdent(dim.Name)
		if err != nil {
			return "", err
		}
		expr = q
	}
	if grain != "" && slices.Contains(dim.Grains, dialect.TimeGrain(grain)) {
		return d.TimeTrunc(expr, dialect.TimeGrain(grain))
	}
	return expr, nil
}

// metricExpr renders a base metric's aggregation.
func metricExpr(d dialect.Dialect, mt semantic.Metric, dims []semantic.Dimension) (string, error) {
	if strings.TrimSpace(mt.SQL) != "" {
		return strings.TrimSpace(mt.SQL), nil
	}
	expr := ""
	if mt.Dimension != "" {
		dim := findDimension(dims, mt.Dimension)
		if dim == nil {
			return "", fmt.Errorf("%w: metric %q references unknown dimension %q", semantic.ErrInvalidExplore, mt.Name, mt.Dimension)
		}
		e, err := dimensionExpr(d, *dim, "")
		if err != nil {
			return "", err
		}
		expr = e
	}
	return d.Aggregate(mt.Aggregation, expr, false)
}

// sourceExpr renders the FROM relation of the base model.
func sourceExpr(d dialect.Dialect, m semantic.Model, opts Options) (string, error) {
	alias, err := d.QuoteIdent(m.Slug)
	if err != nil {
		return "", err
	}
	switch m.Source.Kind {
	case semantic.SourceTable:
		rel, err := d.Qualify(opts.Catalog, m.Source.Namespace, m.Source.Table)
		if err != nil {
			return "", err
		}
		return rel + " AS " + alias, nil
	case semantic.SourceSQL:
		return "(" + strings.TrimSpace(m.Source.SQL) + ") AS " + alias, nil
	}
	return "", fmt.Errorf("%w: model %q has unknown source kind %q", semantic.ErrInvalidExplore, m.Slug, m.Source.Kind)
}

// fieldOwners maps every reachable field to the slug of the model that owns it.
func fieldOwners(base semantic.Model, models map[string]semantic.Model) (map[string]string, error) {
	out := map[string]string{}
	add := func(m semantic.Model) {
		for _, dim := range m.Dimensions {
			out[dim.Name] = m.Slug
		}
		for _, mt := range m.Metrics {
			out[mt.Name] = m.Slug
		}
	}
	add(base)
	seen := map[string]bool{base.Slug: true}
	for _, j := range base.Joins {
		if seen[j.To] {
			continue
		}
		m, ok := models[j.To]
		if !ok {
			return nil, fmt.Errorf("%w: join targets unknown model %q", semantic.ErrInvalidExplore, j.To)
		}
		seen[j.To] = true
		add(m)
	}
	return out, nil
}

func dimensionOf(m semantic.Model, name string) *semantic.Dimension {
	for i := range m.Dimensions {
		if m.Dimensions[i].Name == name {
			return &m.Dimensions[i]
		}
	}
	return nil
}

func metricOf(m semantic.Model, name string) *semantic.Metric {
	for i := range m.Metrics {
		if m.Metrics[i].Name == name {
			return &m.Metrics[i]
		}
	}
	return nil
}

func findDimension(dims []semantic.Dimension, name string) *semantic.Dimension {
	for i := range dims {
		if dims[i].Name == name {
			return &dims[i]
		}
	}
	return nil
}
