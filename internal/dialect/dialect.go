// Package dialect is how Baleia writes SQL for each engine kind: quoting,
// qualification, literals, statement splitting, previews, metadata queries
// and the SQL-generation primitives of the semantic layer (#14, #33, #91).
//
// Every difference between engines is declared here; no other package spells
// engine SQL. Dialects describe what SQL looks like; they never authorize
// anything (that is internal/policy).
package dialect

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Names of the v1 dialects.
const (
	Trino      = "trino"
	PostgreSQL = "postgresql"
	MySQL      = "mysql"
	MariaDB    = "mariadb"
	TSQL       = "tsql"
	Oracle     = "oracle"
	SQLite     = "sqlite"
	ClickHouse = "clickhouse"
	FlightSQL  = "flightsql-generic"
)

var (
	// ErrUnsupported means the dialect cannot express the request; callers
	// report it rather than falling back to another dialect's SQL.
	ErrUnsupported = errors.New("not supported by this dialect")
	// ErrInvalid means the input (identifier, type, value) is malformed.
	ErrInvalid = errors.New("invalid input")
)

// LogicalType is an engine-independent column type: boolean, int64,
// decimal(p,s), float64, string, date, timestamp, timestamptz, binary, json.
type LogicalType string

const (
	TypeBoolean     LogicalType = "boolean"
	TypeInt64       LogicalType = "int64"
	TypeFloat64     LogicalType = "float64"
	TypeString      LogicalType = "string"
	TypeDate        LogicalType = "date"
	TypeTimestamp   LogicalType = "timestamp"
	TypeTimestampTZ LogicalType = "timestamptz"
	TypeBinary      LogicalType = "binary"
	TypeJSON        LogicalType = "json"
)

// Decimal returns the logical type decimal(p,s).
func Decimal(precision, scale int) LogicalType {
	return LogicalType(fmt.Sprintf("decimal(%d,%d)", precision, scale))
}

// decimal parses decimal(p,s). ok reports whether t is a decimal type at
// all; when ok is true, err reports a decimal that is malformed (bad shape
// or out-of-range precision/scale). Callers switch on other types when ok
// is false and must reject the type when ok is true with err != nil.
func (t LogicalType) decimal() (p, s int, ok bool, err error) {
	inner, isDecimal := strings.CutPrefix(string(t), "decimal(")
	if !isDecimal {
		return 0, 0, false, nil
	}
	inner, closed := strings.CutSuffix(inner, ")")
	pStr, sStr, hasComma := strings.Cut(inner, ",")
	// Cut only splits on the first comma, so a decimal with three parts
	// (decimal(10,2,3)) would reach Atoi and be misreported as a range
	// error; the shape check runs first.
	if !closed || !hasComma || strings.Contains(sStr, ",") {
		return 0, 0, true, fmt.Errorf("%w: type %q", ErrInvalid, t)
	}
	p, errP := strconv.Atoi(strings.TrimSpace(pStr))
	s, errS := strconv.Atoi(strings.TrimSpace(sStr))
	if errP != nil || errS != nil || p < 1 || p > 38 || s < 0 || s > p {
		return 0, 0, true, fmt.Errorf("%w: type %q (precision 1-38, scale 0-precision)", ErrInvalid, t)
	}
	return p, s, true, nil
}

// TimeGrain is a TimeTrunc unit.
type TimeGrain string

const (
	GrainSecond  TimeGrain = "second"
	GrainMinute  TimeGrain = "minute"
	GrainHour    TimeGrain = "hour"
	GrainDay     TimeGrain = "day"
	GrainWeek    TimeGrain = "week" // ISO week, starting on Monday
	GrainMonth   TimeGrain = "month"
	GrainQuarter TimeGrain = "quarter"
	GrainYear    TimeGrain = "year"
)

// Grains lists every TimeGrain, finest first.
var Grains = []TimeGrain{GrainSecond, GrainMinute, GrainHour, GrainDay, GrainWeek, GrainMonth, GrainQuarter, GrainYear}

// AggFunc is an Aggregate function.
type AggFunc string

const (
	AggSum                 AggFunc = "sum"
	AggCount               AggFunc = "count"
	AggAvg                 AggFunc = "avg"
	AggMin                 AggFunc = "min"
	AggMax                 AggFunc = "max"
	AggCountDistinct       AggFunc = "count_distinct"
	AggApproxCountDistinct AggFunc = "approx_count_distinct"
)

// Span is one statement of a script: Text is the statement without its
// terminator or surrounding whitespace; Start and End are byte offsets of
// Text in the script.
type Span struct {
	Text       string
	Start, End int
}

// MetadataQueries are the catalog queries of an engine, in the dialect's own
// bind-parameter syntax (Placeholder). Schemas takes no parameter; Tables
// takes the schema; Columns takes the schema and the table. Each returns the
// documented columns in order.
type MetadataQueries struct {
	Schemas string // schema_name
	Tables  string // table_name, table_type ('TABLE' or 'VIEW')
	Columns string // column_name, data_type, is_nullable ('YES'/'NO'), ordinal_position
	// Catalog reads every table of the session's catalog with its columns in
	// one query, for the metadata sync through an engine (#15): table_schema,
	// table_name, table_type ('TABLE' or 'VIEW'), column_name, data_type,
	// is_nullable ('YES'/'NO'), ordinal_position, ordered by schema, table
	// and position; a table without columns has NULL column fields. Empty
	// when the engine has no such query.
	Catalog string
}

// Dialect is how one engine kind spells SQL.
type Dialect interface {
	Name() string

	// QuoteIdent returns s as a delimited identifier. Empty names, NUL bytes
	// and names over the engine's identifier limit are errors; nothing is
	// silently altered.
	QuoteIdent(s string) (string, error)
	// QualifiedName quotes each part and joins them with ".".
	QualifiedName(parts ...string) (string, error)
	// Qualify names an object following the engine's namespace rules:
	// catalogs are dropped where one connection sees one catalog, and a
	// schema path deeper than the engine supports is an error.
	Qualify(catalog string, schema []string, object string) (string, error)
	// QuoteString returns s as a string literal.
	QuoteString(s string) string
	// Literal renders v as a literal of type t (nil is NULL).
	Literal(v any, t LogicalType) (string, error)

	// SplitStatements splits a script into statements.
	SplitStatements(sql string) ([]string, error)
	// Statements is SplitStatements with byte offsets into sql.
	Statements(sql string) ([]Span, error)
	// PreviewSQL selects the first limit rows of an already qualified object.
	PreviewSQL(object string, limit int) string
	// Limit caps the rows of a query by wrapping it.
	Limit(sql string, n int) string
	// ExplainSQL returns the statement that explains sql, or false when the
	// engine has no single-statement EXPLAIN.
	ExplainSQL(sql string) (string, bool)
	// Placeholder is the n-th (1-based) bind parameter.
	Placeholder(n int) string
	MetadataQueries() MetadataQueries

	// SQL-generation primitives. Expressions are SQL the caller built from
	// quoted identifiers; they are not re-checked.
	Aggregate(fn AggFunc, expr string, distinct bool) (string, error)
	TimeTrunc(expr string, grain TimeGrain) (string, error)
	Cast(expr string, t LogicalType) (string, error)
	// SafeDivide divides as floating point and yields NULL for a zero
	// denominator.
	SafeDivide(num, den string) string

	// Editor support.
	Keywords() []string
	Functions() []string
	FormatterLanguage() string // sql-formatter language id
	CodeMirrorBase() string    // @codemirror/lang-sql base dialect
}

// kindToName is the single place that translates engine kinds (the API
// vocabulary) into dialect names (this package's vocabulary): "sqlserver"
// runs T-SQL; "flightsql" and the test fake share the generic dialect.
var kindToName = map[string]string{
	"trino": Trino, "postgresql": PostgreSQL, "mysql": MySQL, "mariadb": MariaDB,
	"sqlserver": TSQL, "oracle": Oracle, "sqlite": SQLite, "clickhouse": ClickHouse,
	"flightsql": FlightSQL, "fake": FlightSQL,
}

// Registry holds every dialect by name (#364 follow-up): built once by
// NewRegistry and only read afterwards, so it is safe for concurrent use.
type Registry struct {
	byName map[string]Dialect
}

// NewRegistry builds the dialects of every engine Baleia supports.
func NewRegistry() *Registry {
	r := &Registry{byName: map[string]Dialect{}}
	for _, d := range []Dialect{
		newTrino(), newPostgreSQL(), newMySQL(), newMariaDB(), newTSQL(),
		newOracle(), newSQLite(), newClickHouse(), newFlightSQL(),
	} {
		r.byName[d.Name()] = d
	}
	return r
}

// Get returns a dialect by name.
func (r *Registry) Get(name string) (Dialect, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// MustGet returns a dialect and panics for an unknown name; use it only with
// the name constants.
func (r *Registry) MustGet(name string) Dialect {
	d, ok := r.byName[name]
	if !ok {
		panic("unknown dialect: " + name)
	}
	return d
}

// ForEngineKind returns the dialect of a query-engine kind.
func (r *Registry) ForEngineKind(kind string) (Dialect, bool) {
	name, ok := kindToName[kind]
	if !ok {
		return nil, false
	}
	return r.Get(name)
}

// Names lists the dialects, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// defaultRegistry backs the package-level lookups below while callers move
// to an injected Registry (#364 follow-up); it goes away with them.
var defaultRegistry = NewRegistry()

// ForEngineKind returns the dialect of a query-engine kind.
func ForEngineKind(kind string) (Dialect, bool) { return defaultRegistry.ForEngineKind(kind) }

// Get returns a dialect by name.
func Get(name string) (Dialect, bool) { return defaultRegistry.Get(name) }

// MustGet returns a dialect and panics for an unknown name; use it only with
// the name constants.
func MustGet(name string) Dialect { return defaultRegistry.MustGet(name) }

// Names lists the dialects, sorted.
func Names() []string { return defaultRegistry.Names() }
