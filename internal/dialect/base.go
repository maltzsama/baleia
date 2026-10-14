package dialect

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// spec is a dialect described by data plus the few functions where engines
// differ in shape. Every v1 dialect is a spec.
type spec struct {
	name string

	// Identifiers.
	identOpen, identClose string // `"`, "`" or "[" "]"
	maxIdent              int    // 0 = unknown
	identBytes            bool   // the limit counts bytes, not characters

	// Strings: backslash also escapes (MySQL, ClickHouse); national prefix N (T-SQL).
	backslashStrings bool
	nationalStrings  bool

	lex lexRules

	// Namespaces: catalogs is false where one connection sees one catalog;
	// schemaDepth is the schema path depth (0: no schemas, SQLite; -1: any).
	catalogs    bool
	schemaDepth int

	placeholder func(n int) string
	preview     func(obj string, n int) string // nil: SELECT * FROM obj LIMIT n
	limit       func(sql string, n int) string // nil: SELECT * FROM (sql) AS baleia_limit LIMIT n
	explain     func(sql string) (string, bool)

	types      map[LogicalType]string // Cast target per type; decimal handled by decimalType
	decimalTyp func(p, s int) string
	castFn     func(expr string, t LogicalType, target string) string // nil: CAST(expr AS target)

	boolLit     func(b bool) string
	dateLit     func(s string) string
	tsLit       func(s string) string // s: YYYY-MM-DD HH:MM:SS[.ffffff]
	tsTZLit     func(s string) string // s in UTC; nil: unsupported
	binaryLit   func(hex string) string
	jsonLit     func(quoted string) string // nil: the quoted string
	decimalLit  func(digits, target string) string
	timeTrunc   func(expr string, g TimeGrain) (string, error)
	approxCount func(expr string) string // nil: exact COUNT(DISTINCT expr)
	floatCast   string                   // SafeDivide numerator cast target; "" when / is already non-integer
	metadata    MetadataQueries

	formatter, codemirror string
}

func (d *spec) Name() string { return d.name }

func (d *spec) QuoteIdent(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty identifier", ErrInvalid)
	}
	if strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("%w: identifier contains a NUL byte", ErrInvalid)
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: identifier is not valid UTF-8", ErrInvalid)
	}
	if n := identLen(s, d.identBytes); d.maxIdent > 0 && n > d.maxIdent {
		return "", fmt.Errorf("%w: identifier is longer than %d %s", ErrInvalid, d.maxIdent, unit(d.identBytes))
	}
	return d.identOpen + strings.ReplaceAll(s, d.identClose, d.identClose+d.identClose) + d.identClose, nil
}

func identLen(s string, bytes bool) int {
	if bytes {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}

func unit(bytes bool) string {
	if bytes {
		return "bytes"
	}
	return "characters"
}

func (d *spec) QualifiedName(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: empty name", ErrInvalid)
	}
	quoted := make([]string, len(parts))
	for i, p := range parts {
		q, err := d.QuoteIdent(p)
		if err != nil {
			return "", err
		}
		quoted[i] = q
	}
	return strings.Join(quoted, "."), nil
}

func (d *spec) Qualify(catalog string, schema []string, object string) (string, error) {
	var parts []string
	switch {
	case d.schemaDepth == 0:
		// SQLite: one database without schemas; catalog and schema are ignored.
		return d.QualifiedName(object)
	case d.schemaDepth > 0 && len(schema) > d.schemaDepth:
		return "", fmt.Errorf("%w: %s supports a schema path of depth %d, got %d", ErrInvalid, d.name, d.schemaDepth, len(schema))
	}
	if d.catalogs && catalog != "" {
		parts = append(parts, catalog)
	}
	parts = append(parts, schema...)
	return d.QualifiedName(append(parts, object)...)
}

func (d *spec) QuoteString(s string) string {
	if d.backslashStrings {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	q := "'" + strings.ReplaceAll(s, "'", "''") + "'"
	if d.nationalStrings {
		return "N" + q
	}
	return q
}

var (
	decimalRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	dateRe    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

func (d *spec) Literal(v any, t LogicalType) (string, error) {
	if v == nil {
		return "NULL", nil
	}
	bad := func() (string, error) {
		return "", fmt.Errorf("%w: %T is not a %s value", ErrInvalid, v, t)
	}
	if p, s, ok, err := t.decimal(); ok {
		if err != nil {
			return "", err
		}
		var digits string
		switch x := v.(type) {
		case string:
			digits = x
		case int, int32, int64:
			digits = fmt.Sprint(x)
		default:
			return bad()
		}
		if !decimalRe.MatchString(digits) {
			return "", fmt.Errorf("%w: %q is not a decimal", ErrInvalid, digits)
		}
		target := d.decimalTyp(p, s)
		if d.decimalLit != nil {
			return d.decimalLit(digits, target), nil
		}
		return "CAST(" + digits + " AS " + target + ")", nil
	}
	switch t {
	case TypeBoolean:
		b, ok := v.(bool)
		if !ok {
			return bad()
		}
		return d.boolLit(b), nil
	case TypeInt64:
		switch x := v.(type) {
		case int, int8, int16, int32, int64, uint8, uint16, uint32:
			return fmt.Sprint(x), nil
		}
		return bad()
	case TypeFloat64:
		var f float64
		switch x := v.(type) {
		case float64:
			f = x
		case float32:
			f = float64(x)
		default:
			return bad()
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("%w: %v has no SQL literal", ErrInvalid, f)
		}
		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		if t, ok := d.types[TypeFloat64]; ok {
			return "CAST(" + s + " AS " + t + ")", nil
		}
		return s, nil
	case TypeString:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		return d.QuoteString(s), nil
	case TypeDate:
		var s string
		switch x := v.(type) {
		case time.Time:
			s = x.Format(time.DateOnly)
		case string:
			if !dateRe.MatchString(x) {
				return "", fmt.Errorf("%w: %q is not YYYY-MM-DD", ErrInvalid, x)
			}
			s = x
		default:
			return bad()
		}
		return d.dateLit(s), nil
	case TypeTimestamp, TypeTimestampTZ:
		ts, ok := v.(time.Time)
		if !ok {
			return bad()
		}
		if t == TypeTimestamp {
			return d.tsLit(ts.Format("2006-01-02 15:04:05.999999")), nil
		}
		if d.tsTZLit == nil {
			return "", fmt.Errorf("%w: %s has no time zone type", ErrUnsupported, d.name)
		}
		return d.tsTZLit(ts.UTC().Format("2006-01-02 15:04:05.999999")), nil
	case TypeBinary:
		b, ok := v.([]byte)
		if !ok {
			return bad()
		}
		return d.binaryLit(strings.ToUpper(hex.EncodeToString(b))), nil
	case TypeJSON:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		if !json.Valid([]byte(s)) {
			return "", fmt.Errorf("%w: not valid JSON", ErrInvalid)
		}
		if d.jsonLit != nil {
			return d.jsonLit(d.QuoteString(s)), nil
		}
		return d.QuoteString(s), nil
	}
	return "", fmt.Errorf("%w: unknown type %q", ErrInvalid, t)
}

func (d *spec) Statements(sql string) ([]Span, error) { return d.lex.split(sql) }

func (d *spec) SplitStatements(sql string) ([]string, error) {
	spans, err := d.lex.split(sql)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Text
	}
	return out, nil
}

func (d *spec) PreviewSQL(object string, n int) string {
	if d.preview != nil {
		return d.preview(object, n)
	}
	return fmt.Sprintf("SELECT * FROM %s LIMIT %d", object, n)
}

// stripTerminator removes trailing whitespace and ';' so a query can be
// wrapped.
func stripTerminator(sql string) string {
	return strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n")
}

func (d *spec) Limit(sql string, n int) string {
	sql = stripTerminator(sql)
	if d.limit != nil {
		return d.limit(sql, n)
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS baleia_limit LIMIT %d", sql, n)
}

func (d *spec) ExplainSQL(sql string) (string, bool) {
	if d.explain == nil {
		return "", false
	}
	return d.explain(stripTerminator(sql))
}

func (d *spec) Placeholder(n int) string { return d.placeholder(n) }

func (d *spec) MetadataQueries() MetadataQueries { return d.metadata }

func (d *spec) Aggregate(fn AggFunc, expr string, distinct bool) (string, error) {
	if strings.TrimSpace(expr) == "" {
		if fn != AggCount || distinct {
			return "", fmt.Errorf("%w: empty expression", ErrInvalid)
		}
		return "COUNT(*)", nil
	}
	dist := ""
	if distinct {
		dist = "DISTINCT "
	}
	switch fn {
	case AggSum, AggCount, AggAvg, AggMin, AggMax:
		return strings.ToUpper(string(fn)) + "(" + dist + expr + ")", nil
	case AggCountDistinct:
		return "COUNT(DISTINCT " + expr + ")", nil
	case AggApproxCountDistinct:
		if d.approxCount != nil {
			return d.approxCount(expr), nil
		}
		return "COUNT(DISTINCT " + expr + ")", nil // exact: the engine has no approximation
	}
	return "", fmt.Errorf("%w: aggregate %q", ErrInvalid, fn)
}

func (d *spec) TimeTrunc(expr string, g TimeGrain) (string, error) {
	switch g {
	case GrainSecond, GrainMinute, GrainHour, GrainDay, GrainWeek, GrainMonth, GrainQuarter, GrainYear:
		return d.timeTrunc(expr, g)
	}
	return "", fmt.Errorf("%w: time grain %q", ErrInvalid, g)
}

func (d *spec) Cast(expr string, t LogicalType) (string, error) {
	var target string
	if p, s, ok, err := t.decimal(); ok {
		if err != nil {
			return "", err
		}
		target = d.decimalTyp(p, s)
	} else {
		known := false
		switch t {
		case TypeBoolean, TypeInt64, TypeFloat64, TypeString, TypeDate, TypeTimestamp, TypeTimestampTZ, TypeBinary, TypeJSON:
			known = true
		}
		if !known {
			return "", fmt.Errorf("%w: unknown type %q", ErrInvalid, t)
		}
		var ok2 bool
		if target, ok2 = d.types[t]; !ok2 {
			return "", fmt.Errorf("%w: %s cannot cast to %s", ErrUnsupported, d.name, t)
		}
	}
	if d.castFn != nil {
		return d.castFn(expr, t, target), nil
	}
	return "CAST(" + expr + " AS " + target + ")", nil
}

func (d *spec) SafeDivide(num, den string) string {
	if d.floatCast != "" {
		num = "CAST(" + num + " AS " + d.floatCast + ")"
	}
	return "(" + num + ") / NULLIF(" + den + ", 0)"
}

func (d *spec) Keywords() []string        { return lists[d.name].keywords }
func (d *spec) Functions() []string       { return lists[d.name].functions }
func (d *spec) FormatterLanguage() string { return d.formatter }
func (d *spec) CodeMirrorBase() string    { return d.codemirror }

// dateTrunc is date_trunc('<grain>', expr), shared by engines that have it.
func dateTrunc(expr string, g TimeGrain) (string, error) {
	return "date_trunc('" + string(g) + "', " + expr + ")", nil
}

func numbered(prefix string) func(int) string {
	return func(n int) string { return prefix + strconv.Itoa(n) }
}

func question(int) string { return "?" }
