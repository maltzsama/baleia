package datapolicy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/maltzsama/sqlglot-go/expressions"
)

var testColumns = []string{"region", "amount", "email", "cpf", "ts", "deleted", "owner", "a", "b"}

func TestCompile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "comparison and boolean", input: "region = 'EU' and amount > 10", expected: "region = 'EU' AND amount > 10"},
		{name: "in list and not", input: "region IN ('EU', 'US') AND NOT deleted", expected: "region IN ('EU', 'US') AND NOT deleted"},
		{name: "case and coalesce", input: "CASE WHEN a IS NULL THEN 'x' ELSE coalesce(b, 'y') END", expected: "CASE WHEN a IS NULL THEN 'x' ELSE COALESCE(b, 'y') END"},
		{name: "partial mask", input: "concat(substr(email, 1, 3), '***')", expected: "CONCAT(SUBSTR(email, 1, 3), '***')"},
		{name: "keep last characters", input: "lpad(substr(cpf, length(cpf) - 1), 11, '*')", expected: "LPAD(SUBSTR(cpf, LENGTH(cpf) - 1), 11, '*')"},
		{name: "sha256 mask", input: "to_hex(sha256(to_utf8(email)))", expected: "TO_HEX(SHA256(TO_UTF8(email)))"},
		{name: "date truncation and literal", input: "date_trunc('day', ts) = DATE '2020-01-01'", expected: "DATE_TRUNC('DAY', ts) = CAST('2020-01-01' AS DATE)"},
		{name: "date truncation of a date", input: "date_trunc('month', CAST(ts AS DATE)) = DATE '2020-01-01'", expected: "DATE_TRUNC('MONTH', CAST(ts AS DATE)) = CAST('2020-01-01' AS DATE)"},
		{name: "cast with parameters", input: "cast(amount AS decimal(10, 2)) > 1", expected: "CAST(amount AS DECIMAL(10, 2)) > 1"},
		{name: "null constant mask", input: "NULL", expected: "NULL"},
		{name: "string constant mask", input: "'***'", expected: "'***'"},
		{name: "comments are dropped", input: "region = 'EU' -- ) OR 1=1", expected: "region = 'EU'"},
		{name: "quoted column", input: `"Region" = 'EU'`, expected: `"Region" = 'EU'`},
		{name: "distinct from", input: "owner IS DISTINCT FROM 'x'", expected: "owner IS DISTINCT FROM 'x'"},
		{name: "like with escape", input: "email LIKE '%!_x' ESCAPE '!'", expected: "email LIKE '%!_x' ESCAPE '!'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Compile(tt.input, testColumns)
			if err != nil {
				t.Fatalf("Compile(%q) error = %v", tt.input, err)
			}
			if got != tt.expected {
				t.Fatalf("Compile(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCompile_Refused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		reason Reason
		detail string
	}{
		{name: "empty", input: "  ", reason: ReasonEmpty},
		{name: "too long", input: "a = '" + strings.Repeat("x", MaxExpressionLength) + "'", reason: ReasonTooLong},
		{name: "syntax error", input: "a = = 1", reason: ReasonSyntax},
		{name: "second statement", input: "a = 1; DROP TABLE t", reason: ReasonNotAnExpression},
		{name: "from clause", input: "1 FROM other", reason: ReasonNotAnExpression},
		{name: "where clause", input: "1 WHERE a = 1", reason: ReasonNotAnExpression},
		{name: "two projections", input: "a, b", reason: ReasonNotAnExpression},
		{name: "alias", input: "a AS b", reason: ReasonNotAllowed, detail: "alias"},
		{name: "subquery", input: "a IN (SELECT x FROM t)", reason: ReasonNotAllowed, detail: "subquery"},
		{name: "exists", input: "EXISTS (SELECT 1)", reason: ReasonFunctionNotAllow, detail: "exists"},
		{name: "any subquery", input: "a = ANY (SELECT 1)", reason: ReasonNotAllowed, detail: "any"},
		{name: "qualified column", input: "t.a = 1", reason: ReasonQualifiedColumn, detail: "a"},
		{name: "catalog-qualified column", input: "x.y.a = 1", reason: ReasonQualifiedColumn, detail: "a"},
		{name: "unknown column", input: "salary > 1", reason: ReasonUnknownColumn, detail: "salary"},
		{name: "current_user", input: "owner = current_user", reason: ReasonFunctionNotAllow, detail: "currentuser"},
		{name: "current_schema as column", input: "current_schema = a", reason: ReasonFunctionNotAllow, detail: "current_schema"},
		{name: "current_role as column", input: "current_role = a", reason: ReasonFunctionNotAllow, detail: "current_role"},
		{name: "clock", input: "now() > ts", reason: ReasonFunctionNotAllow, detail: "currenttimestamp"},
		{name: "localtimestamp", input: "localtimestamp > ts", reason: ReasonFunctionNotAllow, detail: "localtimestamp"},
		{name: "random", input: "random() > 0.5", reason: ReasonFunctionNotAllow, detail: "random"},
		{name: "aggregate", input: "max(a) > 1", reason: ReasonFunctionNotAllow, detail: "max"},
		{name: "window", input: "row_number() OVER () = 1", reason: ReasonNotAllowed, detail: "window"},
		{name: "user-defined function", input: "my_udf(a) = 1", reason: ReasonFunctionNotAllow, detail: "my_udf"},
		{name: "qualified function", input: "system.builtin.lower(a) = 'x'", reason: ReasonNotAllowed, detail: "dot"},
		{name: "parameter", input: "a = ?", reason: ReasonNotAllowed, detail: "placeholder"},
		{name: "star", input: "*", reason: ReasonNotAllowed, detail: "star"},
		{name: "interval", input: "ts > ts - INTERVAL '1' DAY", reason: ReasonNotAllowed, detail: "interval"},
		{name: "subscript", input: "a[1] = 2", reason: ReasonNotAllowed, detail: "bracket"},
		{name: "row type cast", input: "cast(a AS row(x int))", reason: ReasonNotAllowed, detail: "columndef"},
		{name: "json function", input: "json_extract(a, '$.x') = 1", reason: ReasonFunctionNotAllow, detail: "jsonextract"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Compile(tt.input, testColumns)
			var exprErr *ExprError
			if !errors.As(err, &exprErr) {
				t.Fatalf("Compile(%q) error = %v, want an *ExprError", tt.input, err)
			}
			if exprErr.Reason != tt.reason {
				t.Fatalf("Compile(%q) reason = %q (%v), want %q", tt.input, exprErr.Reason, err, tt.reason)
			}
			if tt.detail != "" && exprErr.Detail != tt.detail {
				t.Fatalf("Compile(%q) detail = %q, want %q", tt.input, exprErr.Detail, tt.detail)
			}
		})
	}
}

func TestCompile_NilColumnsSkipsColumnCheck(t *testing.T) {
	t.Parallel()
	got, err := Compile("salary > 1", nil)
	if err != nil {
		t.Fatalf("Compile with nil columns error = %v", err)
	}
	if got != "salary > 1" {
		t.Fatalf("Compile = %q, want %q", got, "salary > 1")
	}
}

func TestCompile_ColumnsAreCaseInsensitive(t *testing.T) {
	t.Parallel()
	if _, err := Compile("REGION = 'EU'", []string{"Region"}); err != nil {
		t.Fatalf("Compile error = %v", err)
	}
}

// TestAllowedFunctionKinds pins what the sample calls of allowedFunctionCalls
// map to. A change here means the parser maps a function differently, or the
// allow-list changed: both need review (ADR 0028).
func TestAllowedFunctionKinds(t *testing.T) {
	t.Parallel()
	gotKinds := make([]string, 0, len(allowedFunctionKinds))
	for k := range allowedFunctionKinds {
		gotKinds = append(gotKinds, expressions.ClassName(k))
	}
	slices.Sort(gotKinds)
	expectedKinds := []string{
		"Abs", "Ceil", "Concat", "DateAdd", "DateDiff", "DateTrunc", "Day", "Encode", "Floor", "Greatest", "Least",
		"Lower", "Month", "Quarter", "RegexpExtract", "RegexpLike", "RegexpReplace", "Replace", "Reverse",
		"Round", "SHA2Digest", "Sign", "StrPosition", "Substring", "TimestampTrunc", "Trim", "Trunc", "Upper", "Year",
	}
	if !slices.Equal(gotKinds, expectedKinds) {
		t.Fatalf("allowed function kinds = %v, want %v", gotKinds, expectedKinds)
	}

	gotAnonymous := make([]string, 0, len(allowedAnonymous))
	for name := range allowedAnonymous {
		gotAnonymous = append(gotAnonymous, name)
	}
	slices.Sort(gotAnonymous)
	expectedAnonymous := []string{"length", "lpad", "ltrim", "nullif", "rpad", "rtrim", "split_part", "starts_with", "to_hex"}
	if !slices.Equal(gotAnonymous, expectedAnonymous) {
		t.Fatalf("allowed anonymous functions = %v, want %v", gotAnonymous, expectedAnonymous)
	}
}

func FuzzCompile(f *testing.F) {
	for _, seed := range []string{"region = 'EU'", "to_hex(sha256(to_utf8(email)))", "a IN (SELECT 1)", "1 FROM t", "a; b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		canonical, err := Compile(input, testColumns)
		if err != nil {
			return
		}
		// An accepted expression's canonical form is itself accepted and stable.
		again, err := Compile(canonical, testColumns)
		if err != nil {
			t.Fatalf("canonical %q of %q refused: %v", canonical, input, err)
		}
		if again != canonical {
			t.Fatalf("canonical form not stable: %q -> %q", canonical, again)
		}
	})
}
