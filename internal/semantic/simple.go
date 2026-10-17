// Package semantic is the v1 semantic layer (#33, D9 / ADR 0012): structured,
// per-dialect metric definitions rendered through internal/dialect's
// SQL-generation primitives.
//
// A definition never contains SQL for the common case: it names an aggregation
// over a column, filters and a time grain, and Render turns it into SQL for the
// target binding's dialect. The escape hatch is custom_sql, which is explicitly
// non-portable and must pass policy.ForbiddenInEditor at write time.
//
// The package never talks to an engine and never authorizes anything: the API
// validates the definition with a read-only session (internal/queryengine) and
// the authz package decides who may write.
package semantic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
)

// FormatVersion is the definition version stored in
// semantic_metrics.format_version.
const FormatVersion = 1

// DefinitionType is the only v1 metric kind. Models, joins and the full
// compiler are v2 (#91).
const DefinitionType = "simple"

var (
	// ErrInvalid means the definition cannot be understood or rendered.
	ErrInvalid = errors.New("invalid metric definition")
	// ErrUnsupported means the dialect cannot express the metric.
	ErrUnsupported = dialect.ErrUnsupported
)

// SimpleMetricV1 is one aggregation over one table, optionally filtered and
// truncated by time. The struct is the on-disk format (semantic_metrics.
// definition) and is validated with DisallowUnknownFields.
type SimpleMetricV1 struct {
	Type        string              `json:"type"`
	Aggregation dialect.AggFunc     `json:"aggregation,omitempty"`
	Column      *string             `json:"column,omitempty"`
	Filters     []Filter            `json:"filters,omitempty"`
	TimeColumn  *string             `json:"time_column,omitempty"`
	TimeGrains  []dialect.TimeGrain `json:"time_grains,omitempty"`
	Format      *Format             `json:"format,omitempty"`
	// CustomSQL is a raw aggregate expression in the binding's dialect. When
	// set, Aggregation and Column are ignored and the metric is non-portable.
	CustomSQL *string `json:"custom_sql,omitempty"`
}

// Filter is one predicate on a column.
type Filter struct {
	Column string   `json:"column"`
	Op     FilterOp `json:"op"`
	Values []any    `json:"values,omitempty"`
}

// FilterOp is a supported comparison operator.
type FilterOp string

const (
	OpEq        FilterOp = "="
	OpNeq       FilterOp = "!="
	OpGt        FilterOp = ">"
	OpGte       FilterOp = ">="
	OpLt        FilterOp = "<"
	OpLte       FilterOp = "<="
	OpIn        FilterOp = "in"
	OpNotIn     FilterOp = "not_in"
	OpIsNull    FilterOp = "is_null"
	OpIsNotNull FilterOp = "is_not_null"
)

// Format is display-only metadata; it never changes the generated SQL.
type Format struct {
	Type     string `json:"type"`
	Decimals int    `json:"decimals"`
}

var supportedAggregations = map[dialect.AggFunc]bool{
	dialect.AggSum:           true,
	dialect.AggCount:         true,
	dialect.AggCountDistinct: true,
	dialect.AggAvg:           true,
	dialect.AggMin:           true,
	dialect.AggMax:           true,
}

var supportedOps = map[FilterOp]bool{
	OpEq: true, OpNeq: true, OpGt: true, OpGte: true, OpLt: true, OpLte: true,
	OpIn: true, OpNotIn: true, OpIsNull: true, OpIsNotNull: true,
}

// ParseSimpleMetricV1 decodes and validates a stored definition, rejecting
// unknown fields so a typo cannot silently change a metric's meaning.
func ParseSimpleMetricV1(raw []byte) (SimpleMetricV1, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m SimpleMetricV1
	if err := dec.Decode(&m); err != nil {
		return SimpleMetricV1{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := m.Validate(); err != nil {
		return SimpleMetricV1{}, err
	}
	return m, nil
}

// Validate checks every dialect-independent rule. The dialect-specific rules
// (custom_sql policy and rendering) are checked by the API, where the target
// binding is known.
func (m SimpleMetricV1) Validate() error {
	if m.Type != DefinitionType {
		return fmt.Errorf("%w: type must be %q", ErrInvalid, DefinitionType)
	}
	if m.CustomSQL != nil {
		cs := strings.TrimSpace(*m.CustomSQL)
		if cs == "" {
			return fmt.Errorf("%w: custom_sql is empty", ErrInvalid)
		}
		if strings.Contains(cs, ";") {
			return fmt.Errorf("%w: custom_sql must be a single expression", ErrInvalid)
		}
	} else {
		if !supportedAggregations[m.Aggregation] {
			return fmt.Errorf("%w: aggregation %q", ErrInvalid, m.Aggregation)
		}
		if m.Column == nil || strings.TrimSpace(*m.Column) == "" {
			if m.Aggregation != dialect.AggCount {
				return fmt.Errorf("%w: column is required for %s", ErrInvalid, m.Aggregation)
			}
		}
	}
	for i, f := range m.Filters {
		if strings.TrimSpace(f.Column) == "" {
			return fmt.Errorf("%w: filters[%d].column is empty", ErrInvalid, i)
		}
		if !supportedOps[f.Op] {
			return fmt.Errorf("%w: filters[%d].op %q", ErrInvalid, i, f.Op)
		}
		switch f.Op {
		case OpIsNull, OpIsNotNull:
			if len(f.Values) != 0 {
				return fmt.Errorf("%w: filters[%d] %s takes no values", ErrInvalid, i, f.Op)
			}
		case OpIn, OpNotIn:
			if len(f.Values) == 0 {
				return fmt.Errorf("%w: filters[%d] %s needs values", ErrInvalid, i, f.Op)
			}
		default:
			if len(f.Values) != 1 {
				return fmt.Errorf("%w: filters[%d] %s takes exactly one value", ErrInvalid, i, f.Op)
			}
		}
	}
	for _, g := range m.TimeGrains {
		if !slices.Contains(dialect.Grains, g) {
			return fmt.Errorf("%w: time grain %q", ErrInvalid, g)
		}
	}
	if m.Format != nil {
		if m.Format.Type != "" && m.Format.Type != "number" {
			return fmt.Errorf("%w: format.type %q", ErrInvalid, m.Format.Type)
		}
		if m.Format.Decimals < 0 || m.Format.Decimals > 10 {
			return fmt.Errorf("%w: format.decimals must be 0 to 10", ErrInvalid)
		}
	}
	return nil
}

// TableRef names the metric's table in the target binding's engine.
type TableRef struct {
	Catalog string   // binding.engine_catalog_name ("" = engine default)
	Schema  []string // namespace parts
	Table   string
}

// RenderOptions tune one rendering.
type RenderOptions struct {
	// Alias is the result column name (usually the metric slug). Required.
	Alias string
	// Grain asks for the time axis; empty means one scalar row.
	Grain dialect.TimeGrain
	// From and To bound TimeColumn when Grain is set (half-open: >= From, < To).
	From *time.Time
	To   *time.Time
	// Limit caps the rows of the rendered query; 0 leaves it uncapped.
	Limit int
}

// Render builds the metric query for one binding's dialect. Every identifier
// is quoted by the dialect and every filter value becomes a dialect literal;
// nothing is concatenated from user input.
func Render(m SimpleMetricV1, t TableRef, d dialect.Dialect, opts RenderOptions) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if d == nil {
		return "", fmt.Errorf("%w: no dialect", ErrInvalid)
	}
	if strings.TrimSpace(opts.Alias) == "" {
		return "", fmt.Errorf("%w: alias is required", ErrInvalid)
	}
	from, err := d.Qualify(t.Catalog, t.Schema, t.Table)
	if err != nil {
		return "", err
	}
	alias, err := d.QuoteIdent(opts.Alias)
	if err != nil {
		return "", err
	}

	var selects []string
	if opts.Grain != "" {
		if m.TimeColumn == nil {
			return "", fmt.Errorf("%w: grain requested without a time column", ErrInvalid)
		}
		tc, err := d.QuoteIdent(*m.TimeColumn)
		if err != nil {
			return "", err
		}
		period, err := d.TimeTrunc(tc, opts.Grain)
		if err != nil {
			return "", err
		}
		periodAlias, err := d.QuoteIdent("period")
		if err != nil {
			return "", err
		}
		selects = append(selects, period+" AS "+periodAlias)
	}

	value, err := valueExpression(m, d)
	if err != nil {
		return "", err
	}
	selects = append(selects, value+" AS "+alias)

	var where []string
	for _, f := range m.Filters {
		col, err := d.QuoteIdent(f.Column)
		if err != nil {
			return "", err
		}
		cond, err := renderFilter(d, col, f)
		if err != nil {
			return "", err
		}
		where = append(where, cond)
	}
	if opts.Grain != "" && (opts.From != nil || opts.To != nil) {
		tc, err := d.QuoteIdent(*m.TimeColumn)
		if err != nil {
			return "", err
		}
		if opts.From != nil {
			lit, err := timestampLiteral(d, *opts.From)
			if err != nil {
				return "", err
			}
			where = append(where, tc+" >= "+lit)
		}
		if opts.To != nil {
			lit, err := timestampLiteral(d, *opts.To)
			if err != nil {
				return "", err
			}
			where = append(where, tc+" < "+lit)
		}
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
	if opts.Grain != "" {
		b.WriteString(" GROUP BY 1 ORDER BY 1")
	}
	sql := b.String()
	if opts.Limit > 0 {
		sql = d.Limit(sql, opts.Limit)
	}
	return sql, nil
}

// valueExpression renders the aggregate (or the custom_sql escape hatch).
func valueExpression(m SimpleMetricV1, d dialect.Dialect) (string, error) {
	if m.CustomSQL != nil {
		return strings.TrimSpace(*m.CustomSQL), nil
	}
	expr := ""
	if m.Column != nil && strings.TrimSpace(*m.Column) != "" {
		col, err := d.QuoteIdent(*m.Column)
		if err != nil {
			return "", err
		}
		expr = col
	}
	return d.Aggregate(m.Aggregation, expr, false)
}

// renderFilter renders one predicate; values are dialect literals, never raw.
func renderFilter(d dialect.Dialect, col string, f Filter) (string, error) {
	switch f.Op {
	case OpIsNull:
		return col + " IS NULL", nil
	case OpIsNotNull:
		return col + " IS NOT NULL", nil
	case OpIn, OpNotIn:
		items := make([]string, 0, len(f.Values))
		for _, v := range f.Values {
			lit, err := valueLiteral(d, v)
			if err != nil {
				return "", err
			}
			items = append(items, lit)
		}
		op := " IN ("
		if f.Op == OpNotIn {
			op = " NOT IN ("
		}
		return col + op + strings.Join(items, ", ") + ")", nil
	default:
		lit, err := valueLiteral(d, f.Values[0])
		if err != nil {
			return "", err
		}
		return col + " " + string(f.Op) + " " + lit, nil
	}
}

// valueLiteral maps a JSON-decoded value to a typed dialect literal.
func valueLiteral(d dialect.Dialect, v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		return d.Literal(x, dialect.TypeBoolean)
	case string:
		return d.Literal(x, dialect.TypeString)
	case float64:
		if !math.IsNaN(x) && !math.IsInf(x, 0) && x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return d.Literal(int64(x), dialect.TypeInt64)
		}
		return d.Literal(x, dialect.TypeFloat64)
	case int:
		return d.Literal(int64(x), dialect.TypeInt64)
	case int64:
		return d.Literal(x, dialect.TypeInt64)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return d.Literal(i, dialect.TypeInt64)
		}
		f, err := x.Float64()
		if err != nil {
			return "", fmt.Errorf("%w: %q is not a number", ErrInvalid, x)
		}
		return d.Literal(f, dialect.TypeFloat64)
	default:
		return "", fmt.Errorf("%w: unsupported filter value %T", ErrInvalid, v)
	}
}

// timestampLiteral prefers a time-zone literal and falls back to a plain
// timestamp on engines that have no time-zone type.
func timestampLiteral(d dialect.Dialect, t time.Time) (string, error) {
	lit, err := d.Literal(t, dialect.TypeTimestampTZ)
	if errors.Is(err, dialect.ErrUnsupported) {
		return d.Literal(t, dialect.TypeTimestamp)
	}
	return lit, err
}
