package semantic

// Semantic layer v2 (#91): models, dimensions, metrics and joins, plus the
// explore query agents and the UI send. Nothing here talks to an engine; the
// compiler (internal/semantic/compiler) renders an explore query through
// internal/dialect, and the API validates against a read-only session.
//
// v1 (simple.go) stays as the compatibility shape: a v1 metric compiles to a
// model with one dimension and one metric (migration, #91).

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
)

// SourceKind is how a model names its relation.
type SourceKind string

const (
	SourceTable SourceKind = "table"
	SourceSQL   SourceKind = "sql"
)

// Relationship is a join's cardinality from the model that declares it.
type Relationship string

const (
	ManyToOne Relationship = "many_to_one"
	OneToOne  Relationship = "one_to_one"
	OneToMany Relationship = "one_to_many"
)

// JoinType is the SQL join kind; empty means LEFT (keeps unmatched base rows).
const (
	JoinInner = "inner"
	JoinLeft  = "left"
)

var (
	// ErrInvalidExplore wraps every validation failure of a model or an
	// explore query, so callers can map it to one API error code.
	ErrInvalidExplore = errors.New("invalid explore query")
)

// Model is a queryable relation (table or SQL) with dimensions and metrics.
type Model struct {
	Slug        string      `json:"slug"`
	Label       string      `json:"label,omitempty"`
	Description string      `json:"description,omitempty"`
	CatalogID   string      `json:"catalog_id"`
	BindingID   string      `json:"binding_id"`
	Source      ModelSource `json:"source"`
	PrimaryKey  []string    `json:"primary_key,omitempty"`
	Dimensions  []Dimension `json:"dimensions,omitempty"`
	Metrics     []Metric    `json:"metrics,omitempty"`
	// CalculatedFields are outer-query expressions over dimension/metric
	// aliases; they are not raw input from an agent.
	CalculatedFields []CalculatedField `json:"calculated_fields,omitempty"`
	Joins            []Join            `json:"joins,omitempty"`
}

// ModelSource is a table reference or a SQL query in the binding's dialect.
type ModelSource struct {
	Kind      SourceKind `json:"kind"`
	Namespace []string   `json:"namespace,omitempty"`
	Table     string     `json:"table,omitempty"`
	SQL       string     `json:"sql,omitempty"`
}

// Dimension is a column or SQL expression with a logical type. Time dimensions
// declare the grains they support.
type Dimension struct {
	Name        string              `json:"name"`
	Label       string              `json:"label,omitempty"`
	Description string              `json:"description,omitempty"`
	SQL         string              `json:"sql,omitempty"` // empty: the column named Name
	Type        dialect.LogicalType `json:"type"`
	Grains      []dialect.TimeGrain `json:"grains,omitempty"`
	Hidden      bool                `json:"hidden,omitempty"`
	// Format is display-only, as on metrics; it never changes the SQL.
	Format *Format `json:"format,omitempty"`
}

// Metric is an aggregation over a dimension (or a raw SQL aggregate) or a
// derived metric over other metrics.
type Metric struct {
	Name        string          `json:"name"`
	Label       string          `json:"label,omitempty"`
	Aggregation dialect.AggFunc `json:"aggregation,omitempty"`
	Dimension   string          `json:"dimension,omitempty"`
	SQL         string          `json:"sql,omitempty"`
	Filters     []Filter        `json:"filters,omitempty"`
	Derived     *DerivedMetric  `json:"derived,omitempty"`
	Format      *Format         `json:"format,omitempty"`
	// MigratedFrom is the id of the v1 metric this one was migrated from
	// (#91, #121). It never changes the SQL; the v1 page redirects by it.
	MigratedFrom string `json:"migrated_from,omitempty"`
}

// CalculatedField is an expression over the query result (dimension/metric
// aliases), evaluated in the outer query. The model author owns the SQL; an
// explore query only selects the field by name.
type CalculatedField struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	SQL   string `json:"sql"`
}

// DerivedMetric is arithmetic over two metrics, rendered with SafeDivide for
// division so a zero denominator yields NULL instead of an error.
type DerivedMetric struct {
	Left  string `json:"left"`
	Op    string `json:"op"` // + - * /
	Right string `json:"right"`
}

// Join connects this model to another model. Every joined model must be
// reachable from the same binding (#91).
type Join struct {
	To           string       `json:"to"`
	Relationship Relationship `json:"relationship"`
	Type         string       `json:"type,omitempty"` // inner | left; empty = left
	On           []JoinKey    `json:"on"`
}

// JoinKey is one column pair of a join condition.
type JoinKey struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ExploreFilter filters a dimension (WHERE) or a metric (HAVING).
type ExploreFilter struct {
	Field  string   `json:"field"`
	Op     FilterOp `json:"op"`
	Values []any    `json:"values,omitempty"`
}

// Sort orders a selected dimension or metric.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// ExploreQuery is the only input agents and the UI send to the compiler; it
// never carries raw SQL.
type ExploreQuery struct {
	Model      string          `json:"model"`
	Dimensions []string        `json:"dimensions,omitempty"`
	Metrics    []string        `json:"metrics,omitempty"`
	Filters    []ExploreFilter `json:"filters,omitempty"`
	Sorts      []Sort          `json:"sorts,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	TimeGrain  string          `json:"time_grain,omitempty"`
}

// MaxExploreLimit caps the rows an explore query may request.
const MaxExploreLimit = 10000

// Validate checks a model is internally consistent. modelSet supplies the
// models a join may target; a nil set skips join-target checks.
func (m Model) Validate(modelSet map[string]Model) error {
	if strings.TrimSpace(m.Slug) == "" {
		return fmt.Errorf("%w: model slug is required", ErrInvalidExplore)
	}
	if m.CatalogID == "" {
		return fmt.Errorf("%w: model %q needs catalog_id", ErrInvalidExplore, m.Slug)
	}
	switch m.Source.Kind {
	case SourceTable:
		if m.Source.Table == "" {
			return fmt.Errorf("%w: model %q has a table source without a table", ErrInvalidExplore, m.Slug)
		}
	case SourceSQL:
		if strings.TrimSpace(m.Source.SQL) == "" {
			return fmt.Errorf("%w: model %q has a sql source without SQL", ErrInvalidExplore, m.Slug)
		}
	default:
		return fmt.Errorf("%w: model %q has unknown source kind %q", ErrInvalidExplore, m.Slug, m.Source.Kind)
	}
	if len(m.PrimaryKey) == 0 {
		return fmt.Errorf("%w: model %q needs a primary key", ErrInvalidExplore, m.Slug)
	}

	dimensions := map[string]bool{}
	for _, d := range m.Dimensions {
		if strings.TrimSpace(d.Name) == "" {
			return fmt.Errorf("%w: model %q has a dimension without a name", ErrInvalidExplore, m.Slug)
		}
		if dimensions[d.Name] {
			return fmt.Errorf("%w: model %q repeats dimension %q", ErrInvalidExplore, m.Slug, d.Name)
		}
		dimensions[d.Name] = true
		if !validLogicalType(d.Type) {
			return fmt.Errorf("%w: dimension %q has unknown type %q", ErrInvalidExplore, d.Name, d.Type)
		}
		if d.Type != dialect.TypeDate && d.Type != dialect.TypeTimestamp && d.Type != dialect.TypeTimestampTZ && len(d.Grains) > 0 {
			return fmt.Errorf("%w: non-time dimension %q declares grains", ErrInvalidExplore, d.Name)
		}
	}

	metrics := map[string]bool{}
	for _, mt := range m.Metrics {
		if strings.TrimSpace(mt.Name) == "" {
			return fmt.Errorf("%w: model %q has a metric without a name", ErrInvalidExplore, m.Slug)
		}
		if metrics[mt.Name] {
			return fmt.Errorf("%w: model %q repeats metric %q", ErrInvalidExplore, m.Slug, mt.Name)
		}
		metrics[mt.Name] = true
		if mt.Derived != nil {
			if mt.Aggregation != "" || mt.SQL != "" || mt.Dimension != "" {
				return fmt.Errorf("%w: metric %q is derived and cannot also aggregate", ErrInvalidExplore, mt.Name)
			}
			if mt.Derived.Left == "" || mt.Derived.Right == "" || !slices.Contains([]string{"+", "-", "*", "/"}, mt.Derived.Op) {
				return fmt.Errorf("%w: metric %q has an invalid derived expression", ErrInvalidExplore, mt.Name)
			}
			continue
		}
		if mt.SQL == "" {
			if mt.Aggregation == "" {
				return fmt.Errorf("%w: metric %q needs an aggregation", ErrInvalidExplore, mt.Name)
			}
			if mt.Dimension != "" && !dimensions[mt.Dimension] {
				return fmt.Errorf("%w: metric %q references unknown dimension %q", ErrInvalidExplore, mt.Name, mt.Dimension)
			}
			if mt.Dimension == "" && mt.Aggregation != dialect.AggCount {
				return fmt.Errorf("%w: metric %q needs a dimension (only count may omit it)", ErrInvalidExplore, mt.Name)
			}
		}
		for _, f := range mt.Filters {
			if !dimensions[f.Column] {
				return fmt.Errorf("%w: metric %q filters on unknown dimension %q", ErrInvalidExplore, mt.Name, f.Column)
			}
		}
	}
	calculated := map[string]bool{}
	for _, cf := range m.CalculatedFields {
		if strings.TrimSpace(cf.Name) == "" {
			return fmt.Errorf("%w: model %q has a calculated field without a name", ErrInvalidExplore, m.Slug)
		}
		if dimensions[cf.Name] || metrics[cf.Name] || calculated[cf.Name] {
			return fmt.Errorf("%w: calculated field %q collides with another field", ErrInvalidExplore, cf.Name)
		}
		if strings.TrimSpace(cf.SQL) == "" {
			return fmt.Errorf("%w: calculated field %q needs SQL", ErrInvalidExplore, cf.Name)
		}
		calculated[cf.Name] = true
	}

	for _, j := range m.Joins {
		if j.To == "" || j.To == m.Slug {
			return fmt.Errorf("%w: model %q has an invalid join target %q", ErrInvalidExplore, m.Slug, j.To)
		}
		if modelSet != nil {
			if _, ok := modelSet[j.To]; !ok {
				return fmt.Errorf("%w: model %q joins unknown model %q", ErrInvalidExplore, m.Slug, j.To)
			}
		}
		switch j.Relationship {
		case ManyToOne, OneToOne, OneToMany:
		default:
			return fmt.Errorf("%w: join %q has unknown relationship %q", ErrInvalidExplore, j.To, j.Relationship)
		}
		if j.Type != "" && j.Type != JoinInner && j.Type != JoinLeft {
			return fmt.Errorf("%w: join %q has unknown type %q", ErrInvalidExplore, j.To, j.Type)
		}
		if len(j.On) == 0 {
			return fmt.Errorf("%w: join %q has no key pairs", ErrInvalidExplore, j.To)
		}
	}
	return nil
}

func validLogicalType(t dialect.LogicalType) bool {
	switch t {
	case dialect.TypeString, dialect.TypeBoolean, dialect.TypeInt64, dialect.TypeFloat64,
		dialect.TypeDate, dialect.TypeTimestamp, dialect.TypeTimestampTZ:
		return true
	}
	return false
}

// Canonical returns the stable JSON encoding used in share links (FD5): struct
// field order, empty fields omitted, no whitespace.
func (q ExploreQuery) Canonical() ([]byte, error) {
	return json.Marshal(q)
}

// EncodeShare serializes q as base64url(canonical JSON) for ?q=.
func (q ExploreQuery) EncodeShare() (string, error) {
	raw, err := q.Canonical()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ParseShare decodes a ?q= value. Unknown fields are rejected.
func ParseShare(s string) (ExploreQuery, error) {
	var q ExploreQuery
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return q, fmt.Errorf("%w: share link is not base64url", ErrInvalidExplore)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		return q, fmt.Errorf("%w: %w", ErrInvalidExplore, err)
	}
	return q, nil
}

// Validate checks an explore query against the models it may reference.
func (q ExploreQuery) Validate(models map[string]Model) error {
	if strings.TrimSpace(q.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidExplore)
	}
	base, ok := models[q.Model]
	if !ok {
		return fmt.Errorf("%w: unknown model %q", ErrInvalidExplore, q.Model)
	}
	if len(q.Dimensions) == 0 && len(q.Metrics) == 0 {
		return fmt.Errorf("%w: select at least one dimension or metric", ErrInvalidExplore)
	}
	if q.Limit < 0 || q.Limit > MaxExploreLimit {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidExplore, MaxExploreLimit)
	}

	fields, err := q.resolveFields(models, base)
	if err != nil {
		return err
	}
	seenDim := map[string]bool{}
	for _, name := range q.Dimensions {
		if seenDim[name] {
			return fmt.Errorf("%w: dimension %q is selected twice", ErrInvalidExplore, name)
		}
		seenDim[name] = true
		f, ok := fields[name]
		if !ok || f.dimension == nil {
			return fmt.Errorf("%w: unknown dimension %q", ErrInvalidExplore, name)
		}
	}
	seenMetric := map[string]bool{}
	for _, name := range q.Metrics {
		if seenMetric[name] {
			return fmt.Errorf("%w: metric %q is selected twice", ErrInvalidExplore, name)
		}
		seenMetric[name] = true
		f, ok := fields[name]
		if !ok || (f.metric == nil && f.calculated == nil) {
			return fmt.Errorf("%w: unknown metric %q", ErrInvalidExplore, name)
		}
	}
	for _, f := range q.Filters {
		if _, ok := fields[f.Field]; !ok {
			return fmt.Errorf("%w: filter references unknown field %q", ErrInvalidExplore, f.Field)
		}
		if len(f.Values) == 0 && f.Op != OpIsNull && f.Op != OpIsNotNull {
			return fmt.Errorf("%w: filter %q has no values", ErrInvalidExplore, f.Field)
		}
	}
	for _, s := range q.Sorts {
		if _, ok := fields[s.Field]; !ok {
			return fmt.Errorf("%w: sort references unknown field %q", ErrInvalidExplore, s.Field)
		}
	}
	if q.TimeGrain != "" {
		grain := dialect.TimeGrain(q.TimeGrain)
		found := false
		for _, name := range q.Dimensions {
			f := fields[name]
			if f == nil {
				continue
			}
			if f.dimension != nil && slices.Contains(f.dimension.Grains, grain) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%w: time_grain %q does not match a selected time dimension", ErrInvalidExplore, q.TimeGrain)
		}
	}
	return nil
}

// RenderFilter renders one explore predicate on an already-quoted expression.
// It reuses the v1 filter renderer so values are always dialect literals.
func RenderFilter(d dialect.Dialect, expr string, op FilterOp, values []any) (string, error) {
	if len(values) == 0 && op != OpIsNull && op != OpIsNotNull {
		return "", fmt.Errorf("%w: filter has no values", ErrInvalidExplore)
	}
	return renderFilter(d, expr, Filter{Op: op, Values: values})
}

// resolvedField is a dimension or metric reachable from the base model. model
// is the model that owns it (the base, or a joined one).
type resolvedField struct {
	name       string
	model      *Model
	dimension  *Dimension
	metric     *Metric
	calculated *CalculatedField
}

// resolveFields maps every dimension/metric name reachable through the base
// model's join tree. A duplicate name across models is an error: the explore
// query names fields without qualifying the model.
func (q ExploreQuery) resolveFields(models map[string]Model, base Model) (map[string]*resolvedField, error) {
	out := map[string]*resolvedField{}
	add := func(m Model) error {
		for i := range m.Dimensions {
			d := &m.Dimensions[i]
			if _, dup := out[d.Name]; dup {
				return fmt.Errorf("%w: field %q is not unique across the join tree", ErrInvalidExplore, d.Name)
			}
			out[d.Name] = &resolvedField{name: d.Name, model: &m, dimension: d}
		}
		for i := range m.Metrics {
			mt := &m.Metrics[i]
			if _, dup := out[mt.Name]; dup {
				return fmt.Errorf("%w: field %q is not unique across the join tree", ErrInvalidExplore, mt.Name)
			}
			out[mt.Name] = &resolvedField{name: mt.Name, model: &m, metric: mt}
		}
		for i := range m.CalculatedFields {
			cf := &m.CalculatedFields[i]
			if _, dup := out[cf.Name]; dup {
				return fmt.Errorf("%w: field %q is not unique across the join tree", ErrInvalidExplore, cf.Name)
			}
			out[cf.Name] = &resolvedField{name: cf.Name, model: &m, calculated: cf}
		}
		return nil
	}
	if err := add(base); err != nil {
		return nil, err
	}
	seen := map[string]bool{base.Slug: true}
	for _, j := range base.Joins {
		if seen[j.To] {
			continue
		}
		m, ok := models[j.To]
		if !ok {
			return nil, fmt.Errorf("%w: join targets unknown model %q", ErrInvalidExplore, j.To)
		}
		seen[j.To] = true
		if err := add(m); err != nil {
			return nil, err
		}
	}
	return out, nil
}
