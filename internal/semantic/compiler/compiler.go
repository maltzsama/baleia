// Package compiler turns an explore query into SQL for one binding's dialect
// (#91). It never accepts raw SQL from the caller: the model carries SQL
// expressions, and every query reaches the engine only through the explore
// query shape.
//
// Joins are fan-out safe by construction. A many_to_one / one_to_one join is
// applied before aggregation, which is safe because the joined side has at most
// one row per key; the query then groups by the selected dimensions. A
// one_to_many join needs per-model pre-aggregation in CTEs and is refused with
// ErrUnsupported until that lands (along with metrics owned by a joined model
// and derived metrics).
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
// compiler slice does not render yet. It is not a dialect limitation.
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
	if err := rejectDerived(q, base, models, owners); err != nil {
		return Result{}, err
	}

	used := map[string]bool{base.Slug: true}
	for _, name := range append(slices.Clone(q.Dimensions), q.Metrics...) {
		used[owners[name]] = true
	}
	for _, f := range q.Filters {
		used[owners[f.Field]] = true
	}
	if len(used) == 1 {
		return compileSingle(q, base, d, opts)
	}
	return compileJoined(q, base, models, owners, used, d, opts)
}

// rejectDerived refuses derived metrics until the outer-query pass lands.
func rejectDerived(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string) error {
	for _, name := range q.Metrics {
		if mt := metricOf(ownerModel(owners[name], base, models), name); mt != nil && mt.Derived != nil {
			return fmt.Errorf("%w: derived metric %q", ErrUnsupported, name)
		}
	}
	for _, f := range q.Filters {
		if mt := metricOf(ownerModel(owners[f.Field], base, models), f.Field); mt != nil && mt.Derived != nil {
			return fmt.Errorf("%w: filter on derived metric %q", ErrUnsupported, f.Field)
		}
	}
	return nil
}

// compileSingle renders a query whose fields all live on the base model.
func compileSingle(q semantic.ExploreQuery, base semantic.Model, d dialect.Dialect, opts Options) (Result, error) {
	selects := make([]string, 0, len(q.Dimensions)+len(q.Metrics))
	groupBy := make([]string, 0, len(q.Dimensions))
	outputPos := map[string]int{}
	for _, name := range q.Dimensions {
		expr, err := dimensionExpr(d, *dimensionOf(base, name), q.TimeGrain)
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
		mt := metricOf(base, name)
		expr, err := metricExpr(d, *mt, base.Dimensions, "")
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
	var where, having []string
	for _, f := range q.Filters {
		if metricOf(base, f.Field) != nil {
			mt := metricOf(base, f.Field)
			expr, err := metricExpr(d, *mt, base.Dimensions, "")
			if err != nil {
				return Result{}, err
			}
			cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
			if err != nil {
				return Result{}, err
			}
			having = append(having, cond)
			continue
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
	return assemble(d, q, selects, groupBy, from, where, having, outputPos, []string{fmt.Sprintf("Compiled against %s", d.Name())})
}

// compileJoined renders a query that reaches dimensions on joined models.
func compileJoined(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string, used map[string]bool, d dialect.Dialect, opts Options) (Result, error) {
	for _, name := range q.Metrics {
		if owners[name] != base.Slug {
			return Result{}, fmt.Errorf("%w: metric %q on a joined model needs CTE pre-aggregation", ErrUnsupported, name)
		}
	}

	joins := map[string]semantic.Join{}
	var ordered []semantic.Join
	for _, j := range base.Joins {
		if !used[j.To] {
			continue
		}
		if j.Relationship == semantic.OneToMany {
			return Result{}, fmt.Errorf("%w: one_to_many join to %q needs pre-aggregation", ErrUnsupported, j.To)
		}
		joins[j.To] = j
		ordered = append(ordered, j)
	}
	for slug := range used {
		if slug == base.Slug {
			continue
		}
		if _, ok := joins[slug]; !ok {
			return Result{}, fmt.Errorf("%w: chained join to %q", ErrUnsupported, slug)
		}
	}

	from, err := sourceExpr(d, base, opts)
	if err != nil {
		return Result{}, err
	}
	baseAlias, err := d.QuoteIdent(base.Slug)
	if err != nil {
		return Result{}, err
	}
	for _, j := range ordered {
		joined, err := sourceExpr(d, models[j.To], opts)
		if err != nil {
			return Result{}, err
		}
		joinedAlias, err := d.QuoteIdent(j.To)
		if err != nil {
			return Result{}, err
		}
		conds := make([]string, 0, len(j.On))
		for _, k := range j.On {
			fromCol, err := d.QuoteIdent(k.From)
			if err != nil {
				return Result{}, err
			}
			toCol, err := d.QuoteIdent(k.To)
			if err != nil {
				return Result{}, err
			}
			conds = append(conds, baseAlias+"."+fromCol+" = "+joinedAlias+"."+toCol)
		}
		kind := "LEFT JOIN"
		if j.Type == semantic.JoinInner {
			kind = "INNER JOIN"
		}
		from += " " + kind + " " + joined + " ON " + strings.Join(conds, " AND ")
	}

	selects := make([]string, 0, len(q.Dimensions)+len(q.Metrics))
	groupBy := make([]string, 0, len(q.Dimensions))
	outputPos := map[string]int{}
	for _, name := range q.Dimensions {
		owner := ownerModel(owners[name], base, models)
		ownerAlias, err := d.QuoteIdent(owner.Slug)
		if err != nil {
			return Result{}, err
		}
		dim := dimensionOf(owner, name)
		expr, err := dimensionExprIn(d, ownerAlias, *dim, q.TimeGrain)
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
		expr, err := metricExpr(d, *metricOf(base, name), base.Dimensions, baseAlias)
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

	var where, having []string
	for _, f := range q.Filters {
		owner := ownerModel(owners[f.Field], base, models)
		ownerAlias, err := d.QuoteIdent(owner.Slug)
		if err != nil {
			return Result{}, err
		}
		if mt := metricOf(owner, f.Field); mt != nil {
			expr, err := metricExpr(d, *mt, base.Dimensions, baseAlias)
			if err != nil {
				return Result{}, err
			}
			cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
			if err != nil {
				return Result{}, err
			}
			having = append(having, cond)
			continue
		}
		expr, err := dimensionExprIn(d, ownerAlias, *dimensionOf(owner, f.Field), "")
		if err != nil {
			return Result{}, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return Result{}, err
		}
		where = append(where, cond)
	}

	notes := []string{
		fmt.Sprintf("Compiled against %s", d.Name()),
		"Joined dimension tables before aggregation: fan-out safe for many_to_one/one_to_one",
	}
	return assemble(d, q, selects, groupBy, from, where, having, outputPos, notes)
}

// assemble writes the shared SELECT/WHERE/GROUP BY/HAVING/ORDER BY/LIMIT shape.
func assemble(d dialect.Dialect, q semantic.ExploreQuery, selects, groupBy []string, from string, where, having []string, outputPos map[string]int, notes []string) (Result, error) {
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
	return Result{SQL: sql, Notes: notes}, nil
}

// dimensionExpr renders an unqualified dimension, truncating time dimensions
// when a grain is requested.
func dimensionExpr(d dialect.Dialect, dim semantic.Dimension, grain string) (string, error) {
	return dimensionExprIn(d, "", dim, grain)
}

// dimensionExprIn renders a dimension qualified with alias ("" leaves it
// unqualified). A SQL dimension is trusted content and is used verbatim.
func dimensionExprIn(d dialect.Dialect, alias string, dim semantic.Dimension, grain string) (string, error) {
	var expr string
	if strings.TrimSpace(dim.SQL) != "" {
		expr = strings.TrimSpace(dim.SQL)
	} else {
		col, err := d.QuoteIdent(dim.Name)
		if err != nil {
			return "", err
		}
		if alias != "" {
			expr = alias + "." + col
		} else {
			expr = col
		}
	}
	if grain != "" && slices.Contains(dim.Grains, dialect.TimeGrain(grain)) {
		return d.TimeTrunc(expr, dialect.TimeGrain(grain))
	}
	return expr, nil
}

// metricExpr renders a base metric's aggregation.
func metricExpr(d dialect.Dialect, mt semantic.Metric, dims []semantic.Dimension, alias string) (string, error) {
	if strings.TrimSpace(mt.SQL) != "" {
		return strings.TrimSpace(mt.SQL), nil
	}
	expr := ""
	if mt.Dimension != "" {
		dim := findDimension(dims, mt.Dimension)
		if dim == nil {
			return "", fmt.Errorf("%w: metric %q references unknown dimension %q", semantic.ErrInvalidExplore, mt.Name, mt.Dimension)
		}
		e, err := dimensionExprIn(d, alias, *dim, "")
		if err != nil {
			return "", err
		}
		expr = e
	}
	return d.Aggregate(mt.Aggregation, expr, false)
}

// sourceExpr renders the FROM relation of a model, aliased by its slug.
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

func ownerModel(slug string, base semantic.Model, models map[string]semantic.Model) semantic.Model {
	if slug == base.Slug {
		return base
	}
	return models[slug]
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
