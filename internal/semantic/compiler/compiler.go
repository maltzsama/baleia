// Package compiler turns an explore query into SQL for one binding's dialect
// (#91). It never accepts raw SQL from the caller: the model carries SQL
// expressions, and every query reaches the engine only through the explore
// query shape.
//
// Joins are fan-out safe by construction. A many_to_one / one_to_one join is
// applied before aggregation, which is safe because the joined side has at most
// one row per key; the query then groups by the selected dimensions. A
// one_to_many join needs per-model pre-aggregation in CTEs and is refused with
// ErrUnsupported until that lands, as are metrics owned by a joined model.
//
// Derived metrics are rendered in an outer query over the aggregated rows, so
// they can divide by a metric without repeating the aggregation. Their
// expression is the model's trusted content, rendered through SafeDivide.
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

// plan is the aggregated (inner) query: dimensions, the base metrics needed by
// the selection or by derived expressions, and their filters.
type plan struct {
	selects  []string
	groupBy  []string
	from     string
	where    []string
	having   []string
	innerPos map[string]int
	notes    []string
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
	// Derived metrics live on the base and may reference base metrics only.
	for _, name := range q.Metrics {
		mt := metricOf(ownerModel(owners[name], base, models), name)
		if mt != nil && mt.Derived != nil && owners[name] != base.Slug {
			return Result{}, fmt.Errorf("%w: derived metric %q on a joined model", ErrUnsupported, name)
		}
	}
	needed, err := neededBaseMetrics(q, base, models, owners)
	if err != nil {
		return Result{}, err
	}

	used := map[string]bool{base.Slug: true}
	for _, name := range append(slices.Clone(q.Dimensions), q.Metrics...) {
		used[owners[name]] = true
	}
	for _, f := range q.Filters {
		used[owners[f.Field]] = true
	}

	var p *plan
	if len(used) == 1 {
		p, err = planSingle(q, base, needed, d, opts)
	} else {
		p, err = planJoined(q, base, models, owners, used, needed, d, opts)
	}
	if err != nil {
		return Result{}, err
	}
	return render(q, base, models, owners, p, d)
}

// neededBaseMetrics lists the base metrics the inner query must aggregate:
// selected ones, the dependencies of selected derived metrics, and metric
// filters. Dependencies must be base metrics (one level of derivation).
func neededBaseMetrics(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		out = append(out, name)
		return nil
	}
	addDerivedDeps := func(owner string, derived *semantic.DerivedMetric) error {
		for _, dep := range []string{derived.Left, derived.Right} {
			if owners[dep] != owner {
				return fmt.Errorf("%w: derived metric dependency %q must live on the same model", ErrUnsupported, dep)
			}
			depMetric := metricOf(ownerModel(owners[dep], base, models), dep)
			if depMetric == nil || depMetric.Derived != nil {
				return fmt.Errorf("%w: derived metric dependency %q must be a base metric", ErrUnsupported, dep)
			}
			if err := add(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range q.Metrics {
		owner := owners[name]
		mt := metricOf(ownerModel(owner, base, models), name)
		if mt == nil {
			continue
		}
		if mt.Derived != nil {
			if err := addDerivedDeps(owner, mt.Derived); err != nil {
				return nil, err
			}
			continue
		}
		if err := add(name); err != nil {
			return nil, err
		}
	}
	for _, f := range q.Filters {
		owner := owners[f.Field]
		mt := metricOf(ownerModel(owner, base, models), f.Field)
		if mt == nil || mt.Derived == nil {
			continue // base metric filters render as HAVING, no extra select
		}
		if err := addDerivedDeps(owner, mt.Derived); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// planSingle builds the inner query for fields that all live on the base.
func planSingle(q semantic.ExploreQuery, base semantic.Model, needed []string, d dialect.Dialect, opts Options) (*plan, error) {
	p := &plan{innerPos: map[string]int{}}
	for _, name := range q.Dimensions {
		expr, err := dimensionExpr(d, *dimensionOf(base, name), q.TimeGrain)
		if err != nil {
			return nil, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		p.selects = append(p.selects, expr+" AS "+alias)
		p.innerPos[name] = len(p.selects)
		p.groupBy = append(p.groupBy, fmt.Sprintf("%d", len(p.selects)))
	}
	for _, name := range needed {
		expr, err := metricExpr(d, *metricOf(base, name), base.Dimensions, "")
		if err != nil {
			return nil, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		p.selects = append(p.selects, expr+" AS "+alias)
		p.innerPos[name] = len(p.selects)
	}
	from, err := sourceExpr(d, base, opts)
	if err != nil {
		return nil, err
	}
	p.from = from
	for _, f := range q.Filters {
		if metricOf(base, f.Field) != nil {
			continue // HAVING, below
		}
		expr, err := dimensionExpr(d, *dimensionOf(base, f.Field), "")
		if err != nil {
			return nil, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return nil, err
		}
		p.where = append(p.where, cond)
	}
	for _, f := range q.Filters {
		mt := metricOf(base, f.Field)
		if mt == nil || mt.Derived != nil {
			continue // derived filters are applied in the outer query
		}
		expr, err := metricExpr(d, *mt, base.Dimensions, "")
		if err != nil {
			return nil, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return nil, err
		}
		p.having = append(p.having, cond)
	}
	p.notes = []string{fmt.Sprintf("Compiled against %s", d.Name())}
	return p, nil
}

// planJoined builds the inner query for fields that reach a joined dimension.
func planJoined(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string, used map[string]bool, needed []string, d dialect.Dialect, opts Options) (*plan, error) {
	for _, name := range q.Metrics {
		if owners[name] != base.Slug {
			return nil, fmt.Errorf("%w: metric %q on a joined model needs CTE pre-aggregation", ErrUnsupported, name)
		}
	}
	joins := map[string]semantic.Join{}
	var ordered []semantic.Join
	for _, j := range base.Joins {
		if !used[j.To] {
			continue
		}
		if j.Relationship == semantic.OneToMany {
			return nil, fmt.Errorf("%w: one_to_many join to %q needs pre-aggregation", ErrUnsupported, j.To)
		}
		joins[j.To] = j
		ordered = append(ordered, j)
	}
	for slug := range used {
		if slug == base.Slug {
			continue
		}
		if _, ok := joins[slug]; !ok {
			return nil, fmt.Errorf("%w: chained join to %q", ErrUnsupported, slug)
		}
	}

	p := &plan{innerPos: map[string]int{}}
	from, err := sourceExpr(d, base, opts)
	if err != nil {
		return nil, err
	}
	baseAlias, err := d.QuoteIdent(base.Slug)
	if err != nil {
		return nil, err
	}
	for _, j := range ordered {
		joined, err := sourceExpr(d, models[j.To], opts)
		if err != nil {
			return nil, err
		}
		joinedAlias, err := d.QuoteIdent(j.To)
		if err != nil {
			return nil, err
		}
		conds := make([]string, 0, len(j.On))
		for _, k := range j.On {
			fromCol, err := d.QuoteIdent(k.From)
			if err != nil {
				return nil, err
			}
			toCol, err := d.QuoteIdent(k.To)
			if err != nil {
				return nil, err
			}
			conds = append(conds, baseAlias+"."+fromCol+" = "+joinedAlias+"."+toCol)
		}
		kind := "LEFT JOIN"
		if j.Type == semantic.JoinInner {
			kind = "INNER JOIN"
		}
		from += " " + kind + " " + joined + " ON " + strings.Join(conds, " AND ")
	}
	p.from = from

	for _, name := range q.Dimensions {
		owner := ownerModel(owners[name], base, models)
		ownerAlias, err := d.QuoteIdent(owner.Slug)
		if err != nil {
			return nil, err
		}
		expr, err := dimensionExprIn(d, ownerAlias, *dimensionOf(owner, name), q.TimeGrain)
		if err != nil {
			return nil, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		p.selects = append(p.selects, expr+" AS "+alias)
		p.innerPos[name] = len(p.selects)
		p.groupBy = append(p.groupBy, fmt.Sprintf("%d", len(p.selects)))
	}
	for _, name := range needed {
		expr, err := metricExpr(d, *metricOf(base, name), base.Dimensions, baseAlias)
		if err != nil {
			return nil, err
		}
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		p.selects = append(p.selects, expr+" AS "+alias)
		p.innerPos[name] = len(p.selects)
	}
	for _, f := range q.Filters {
		owner := ownerModel(owners[f.Field], base, models)
		ownerAlias, err := d.QuoteIdent(owner.Slug)
		if err != nil {
			return nil, err
		}
		if mt := metricOf(owner, f.Field); mt != nil {
			if mt.Derived != nil {
				continue // outer query
			}
			expr, err := metricExpr(d, *mt, base.Dimensions, baseAlias)
			if err != nil {
				return nil, err
			}
			cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
			if err != nil {
				return nil, err
			}
			p.having = append(p.having, cond)
			continue
		}
		expr, err := dimensionExprIn(d, ownerAlias, *dimensionOf(owner, f.Field), "")
		if err != nil {
			return nil, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return nil, err
		}
		p.where = append(p.where, cond)
	}
	p.notes = []string{
		fmt.Sprintf("Compiled against %s", d.Name()),
		"Joined dimension tables before aggregation: fan-out safe for many_to_one/one_to_one",
	}
	return p, nil
}

// render writes the final SQL: one layer when no derived metric is selected,
// otherwise an outer query over the aggregated rows.
func render(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string, p *plan, d dialect.Dialect) (Result, error) {
	derived := map[string]semantic.Metric{}
	for _, name := range q.Metrics {
		if mt := metricOf(ownerModel(owners[name], base, models), name); mt != nil && mt.Derived != nil {
			derived[name] = *mt
		}
	}
	if len(derived) == 0 && !hasDerivedFilter(q, base, models, owners) {
		return assemble(d, q, p.selects, p.groupBy, p.from, p.where, p.having, p.innerPos, p.notes)
	}

	inner := selectSQL(p.selects, p.from, p.where, p.groupBy, p.having)
	outerSelects := make([]string, 0, len(q.Dimensions)+len(q.Metrics))
	outerPos := map[string]int{}
	for _, name := range q.Dimensions {
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return Result{}, err
		}
		outerSelects = append(outerSelects, alias+" AS "+alias)
		outerPos[name] = len(outerSelects)
	}
	for _, name := range q.Metrics {
		alias, err := d.QuoteIdent(name)
		if err != nil {
			return Result{}, err
		}
		if mt, ok := derived[name]; ok {
			expr, err := derivedExpr(d, mt)
			if err != nil {
				return Result{}, err
			}
			outerSelects = append(outerSelects, expr+" AS "+alias)
		} else {
			outerSelects = append(outerSelects, alias+" AS "+alias)
		}
		outerPos[name] = len(outerSelects)
	}
	var outerWhere []string
	for _, f := range q.Filters {
		mt := metricOf(ownerModel(owners[f.Field], base, models), f.Field)
		if mt == nil || mt.Derived == nil {
			continue
		}
		expr, err := derivedExpr(d, *mt)
		if err != nil {
			return Result{}, err
		}
		cond, err := semantic.RenderFilter(d, expr, f.Op, f.Values)
		if err != nil {
			return Result{}, err
		}
		outerWhere = append(outerWhere, cond)
	}
	var order []string
	for _, s := range q.Sorts {
		pos, ok := outerPos[s.Field]
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
	b.WriteString(strings.Join(outerSelects, ", "))
	b.WriteString(" FROM (")
	b.WriteString(inner)
	b.WriteString(") AS baleia_explore")
	if len(outerWhere) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(outerWhere, " AND "))
	}
	if len(order) > 0 {
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(order, ", "))
	}
	sql := b.String()
	if q.Limit > 0 {
		sql = d.Limit(sql, q.Limit)
	}
	return Result{SQL: sql, Notes: p.notes}, nil
}

func hasDerivedFilter(q semantic.ExploreQuery, base semantic.Model, models map[string]semantic.Model, owners map[string]string) bool {
	for _, f := range q.Filters {
		if mt := metricOf(ownerModel(owners[f.Field], base, models), f.Field); mt != nil && mt.Derived != nil {
			return true
		}
	}
	return false
}

// derivedExpr renders arithmetic over two aggregated metric aliases.
func derivedExpr(d dialect.Dialect, mt semantic.Metric) (string, error) {
	dm := mt.Derived
	if dm == nil {
		return "", fmt.Errorf("%w: metric %q is not derived", semantic.ErrInvalidExplore, mt.Name)
	}
	left, err := d.QuoteIdent(dm.Left)
	if err != nil {
		return "", err
	}
	right, err := d.QuoteIdent(dm.Right)
	if err != nil {
		return "", err
	}
	switch dm.Op {
	case "+":
		return left + " + " + right, nil
	case "-":
		return left + " - " + right, nil
	case "*":
		return left + " * " + right, nil
	case "/":
		return d.SafeDivide(left, right), nil
	}
	return "", fmt.Errorf("%w: derived metric %q has unknown operator %q", semantic.ErrInvalidExplore, mt.Name, dm.Op)
}

// assemble writes a single-layer SELECT/WHERE/GROUP BY/HAVING/ORDER BY/LIMIT.
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
	sql := selectSQL(selects, from, where, groupBy, having) + orderClause(order)
	if q.Limit > 0 {
		sql = d.Limit(sql, q.Limit)
	}
	return Result{SQL: sql, Notes: notes}, nil
}

func selectSQL(selects []string, from string, where, groupBy, having []string) string {
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
	return b.String()
}

func orderClause(order []string) string {
	if len(order) == 0 {
		return ""
	}
	return " ORDER BY " + strings.Join(order, ", ")
}

// dimensionExpr renders an unqualified dimension.
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
