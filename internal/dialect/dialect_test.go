package dialect

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAllDialectsMatchRegistry keeps allDialects (which decides the embedded
// word lists) in sync with the registry, so a new dialect cannot ship silently
// empty autocomplete.
func TestAllDialectsMatchRegistry(t *testing.T) {
	got := slices.Clone(allDialects)
	slices.Sort(got)
	if want := testRegistry.Names(); !slices.Equal(got, want) {
		t.Errorf("allDialects = %v, registered = %v", got, want)
	}
}

func TestRegistry(t *testing.T) {
	if got := strings.Join(testRegistry.Names(), ","); got != "clickhouse,flightsql-generic,mariadb,mysql,oracle,postgresql,sqlite,trino,tsql" {
		t.Errorf("names = %s", got)
	}
	for _, n := range allDialects {
		d := testRegistry.MustGet(n)
		if d.Name() != n || d.FormatterLanguage() == "" || d.CodeMirrorBase() == "" {
			t.Errorf("%s: incomplete", n)
		}
		if d.Keywords() == nil || d.Functions() == nil {
			t.Errorf("%s: lists must be non-nil", n)
		}
		m := d.MetadataQueries()
		if m.Schemas == "" || m.Tables == "" || m.Columns == "" {
			t.Errorf("%s: metadata queries missing", n)
		}
		if !strings.Contains(m.Tables, d.Placeholder(1)) || !strings.Contains(m.Columns, d.Placeholder(2)) {
			t.Errorf("%s: metadata queries must use the dialect's placeholders", n)
		}
	}
	if _, ok := testRegistry.Get("nope"); ok {
		t.Error("unknown dialect found")
	}
}

// TestNewRegistry: every registry built lists the same dialects, so callers
// can each be injected their own without disagreeing.
func TestNewRegistry(t *testing.T) {
	a, b := NewRegistry(), NewRegistry()
	if !slices.Equal(a.Names(), b.Names()) || !slices.Equal(a.Names(), testRegistry.Names()) {
		t.Fatalf("names differ: %v / %v / %v", a.Names(), b.Names(), testRegistry.Names())
	}
	for _, n := range a.Names() {
		if a.MustGet(n).Name() != n {
			t.Errorf("%s: wrong dialect", n)
		}
	}
}

// TestMustGetPanicsOnUnknown: MustGet is for the name constants; an unknown
// name is a programming error.
func TestMustGetPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustGet of an unknown dialect did not panic")
		}
	}()
	testRegistry.MustGet("nope")
}

// TestForEngineKind covers the kind → dialect translation: every engine kind
// the API can carry resolves, and a miss is (nil, false) explicitly rather
// than through a lookup of the empty name.
func TestForEngineKind(t *testing.T) {
	for kind, want := range map[string]string{
		"trino": Trino, "postgresql": PostgreSQL, "mysql": MySQL, "mariadb": MariaDB,
		"sqlserver": TSQL, "oracle": Oracle, "sqlite": SQLite, "clickhouse": ClickHouse,
		"flightsql": FlightSQL, "fake": FlightSQL, // the test fake shares the ANSI subset
	} {
		d, ok := testRegistry.ForEngineKind(kind)
		if !ok || d == nil || d.Name() != want {
			t.Errorf("ForEngineKind(%q) = (%v, %v), want %q", kind, d, ok, want)
		}
	}
	for _, kind := range []string{"", "nope", "postgres", "sql-server"} {
		if d, ok := testRegistry.ForEngineKind(kind); ok || d != nil {
			t.Errorf("ForEngineKind(%q) = (%v, %v), want (nil, false)", kind, d, ok)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	want := map[string]string{
		Trino: `"a""b"`, PostgreSQL: `"a""b"`, SQLite: `"a""b"`, FlightSQL: `"a""b"`,
		MySQL: "`a\"b`", MariaDB: "`a\"b`", ClickHouse: "`a\"b`", TSQL: `[a"b]`,
	}
	for _, n := range allDialects {
		d := testRegistry.MustGet(n)
		got, err := d.QuoteIdent(`a"b`)
		if n == Oracle {
			// Oracle has no escape for " inside a quoted identifier (ORA-25716).
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("oracle: %q accepted (%q)", `a"b`, got)
			}
		} else if err != nil || got != want[n] {
			t.Errorf("%s: %q, %v", n, got, err)
		}
		for _, bad := range []string{"", "a\x00b", "\xff"} {
			if _, err := d.QuoteIdent(bad); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: %q accepted", n, bad)
			}
		}
	}
	// The closing quote character is doubled.
	for n, in := range map[string]string{MySQL: "a`b", TSQL: "a]b", ClickHouse: "a`b"} {
		got, _ := testRegistry.MustGet(n).QuoteIdent(in)
		if want := map[string]string{MySQL: "`a``b`", TSQL: "[a]]b]", ClickHouse: "`a``b`"}[n]; got != want {
			t.Errorf("%s: %q", n, got)
		}
	}
	// Identifier limits: PostgreSQL 63 bytes, MySQL 64 characters, Oracle 128 bytes.
	if _, err := testRegistry.MustGet(PostgreSQL).QuoteIdent(strings.Repeat("a", 64)); err == nil {
		t.Error("postgresql: 64 bytes accepted")
	}
	if _, err := testRegistry.MustGet(MySQL).QuoteIdent(strings.Repeat("é", 64)); err != nil {
		t.Errorf("mysql counts characters: %v", err)
	}
	if _, err := testRegistry.MustGet(Oracle).QuoteIdent(strings.Repeat("é", 65)); err == nil {
		t.Error("oracle: 130 bytes accepted")
	}
}

func TestQualify(t *testing.T) {
	cases := map[string]string{
		Trino:      `"c"."s"."t"`,
		PostgreSQL: `"s"."t"`,
		MySQL:      "`s`.`t`",
		MariaDB:    "`s`.`t`",
		TSQL:       "[c].[s].[t]",
		Oracle:     `"s"."t"`,
		SQLite:     `"t"`,
		ClickHouse: "`s`.`t`",
		FlightSQL:  `"c"."s"."t"`,
	}
	for n, want := range cases {
		got, err := testRegistry.MustGet(n).Qualify("c", []string{"s"}, "t")
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", n, got, err)
		}
	}
	if _, err := testRegistry.MustGet(Trino).Qualify("c", []string{"a", "b"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Error("trino accepted a two-level schema")
	}
	if got, err := testRegistry.MustGet(FlightSQL).Qualify("", []string{"a", "b"}, "t"); err != nil || got != `"a"."b"."t"` {
		t.Errorf("flightsql deep path: %q, %v", got, err)
	}
	if got, _ := testRegistry.MustGet(Trino).Qualify("", []string{"s"}, "t"); got != `"s"."t"` {
		t.Errorf("trino without catalog: %q", got)
	}
}

func TestQuoteString(t *testing.T) {
	in := `it's a \ test`
	want := map[string]string{
		Trino: `'it''s a \ test'`, PostgreSQL: `'it''s a \ test'`, Oracle: `'it''s a \ test'`,
		SQLite: `'it''s a \ test'`, FlightSQL: `'it''s a \ test'`,
		MySQL: `'it''s a \\ test'`, MariaDB: `'it''s a \\ test'`, ClickHouse: `'it''s a \\ test'`,
		TSQL: `N'it''s a \ test'`,
	}
	for _, n := range allDialects {
		if got := testRegistry.MustGet(n).QuoteString(in); got != want[n] {
			t.Errorf("%s: %s", n, got)
		}
	}
}

func split(t *testing.T, name, sql string) []string {
	t.Helper()
	out, err := testRegistry.MustGet(name).SplitStatements(sql)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestSplitStatements_common(t *testing.T) {
	for _, n := range allDialects {
		got := split(t, n, "SELECT 1; SELECT 'a;b' -- c;d\n; /* x;y */ SELECT 2;;\n  -- only a comment\n")
		if len(got) != 3 || got[0] != "SELECT 1" || got[1] != "SELECT 'a;b' -- c;d" || got[2] != "/* x;y */ SELECT 2" {
			t.Errorf("%s: %q", n, got)
		}
		if got := split(t, n, `SELECT "a;b" FROM t; SELECT 'it''s;'`); len(got) != 2 {
			t.Errorf("%s: quoted identifier or doubled quote split: %q", n, got)
		}
		if _, err := testRegistry.MustGet(n).SplitStatements("SELECT 'open"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: unterminated string accepted", n)
		}
	}
}

func TestSplitStatements_postgresql(t *testing.T) {
	got := split(t, PostgreSQL, `CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql; DO $body$ BEGIN PERFORM 1; END $body$; SELECT E'a\';b'; SELECT 1`)
	if len(got) != 4 || !strings.HasSuffix(got[0], "LANGUAGE sql") || !strings.HasPrefix(got[1], "DO $body$") || got[2] != `SELECT E'a\';b'` {
		t.Errorf("%q", got)
	}
	if got := split(t, PostgreSQL, "SELECT 1 /* a /* nested; */ still; */; SELECT 2"); len(got) != 2 {
		t.Errorf("nested comment: %q", got)
	}
	if got := split(t, PostgreSQL, "SELECT $1; SELECT 2"); len(got) != 2 {
		t.Errorf("$1 is not a dollar quote: %q", got)
	}
	if got := split(t, PostgreSQL, "SELECT a$b$c FROM t; SELECT 2"); len(got) != 2 {
		t.Errorf("$ inside an identifier is not a dollar quote: %q", got)
	}
}

func TestSplitStatements_mysql(t *testing.T) {
	for _, n := range []string{MySQL, MariaDB} {
		got := split(t, n, "SELECT 'a\\';b', \"c;d\", `e;f` FROM t # hash; comment\n; SELECT 2")
		if len(got) != 2 {
			t.Errorf("%s: %q", n, got)
		}
		_, err := testRegistry.MustGet(n).SplitStatements("DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; END//\nDELIMITER ;")
		if !errors.Is(err, ErrDelimiter) {
			t.Errorf("%s: DELIMITER: %v", n, err)
		}
	}
}

func TestSplitStatements_tsql(t *testing.T) {
	got := split(t, TSQL, "SELECT [a;b], [x]]y;] FROM t\nGO\nCREATE PROCEDURE p AS SELECT 1\n  go  \nSELECT 2; SELECT 3")
	if len(got) != 4 || got[0] != "SELECT [a;b], [x]]y;] FROM t" || got[1] != "CREATE PROCEDURE p AS SELECT 1" {
		t.Errorf("%q", got)
	}
	if got := split(t, TSQL, "SELECT 1 AS GOAL; SELECT 'GO'"); len(got) != 2 {
		t.Errorf("GO inside a line is not a separator: %q", got)
	}
}

func TestSplitStatements_oracle(t *testing.T) {
	got := split(t, Oracle, "SELECT q'[a';b]' FROM dual;\nBEGIN\n  NULL;\n  INSERT INTO t VALUES (1);\nEND;\n/\nCREATE OR REPLACE PROCEDURE p AS BEGIN NULL; END;\n/\nSELECT 2 FROM dual")
	if len(got) != 4 || got[0] != "SELECT q'[a';b]' FROM dual" || !strings.HasPrefix(got[1], "BEGIN") || !strings.HasSuffix(got[1], "END;") ||
		!strings.HasPrefix(got[2], "CREATE OR REPLACE PROCEDURE") || got[3] != "SELECT 2 FROM dual" {
		t.Errorf("%q", got)
	}
	if got := split(t, Oracle, "SELECT q'{x}'||q'<y;>' FROM dual; SELECT 1 FROM dual"); len(got) != 2 {
		t.Errorf("q-quotes: %q", got)
	}
}

func TestStatementsOffsets(t *testing.T) {
	sql := "  SELECT 1;\n\nSELECT 'x;';"
	spans, err := testRegistry.MustGet(Trino).Statements(sql)
	if err != nil || len(spans) != 2 {
		t.Fatal(spans, err)
	}
	for _, s := range spans {
		if sql[s.Start:s.End] != s.Text {
			t.Errorf("span %+v does not match %q", s, sql[s.Start:s.End])
		}
	}
}

func TestPreviewAndLimit(t *testing.T) {
	want := map[string][2]string{
		Trino:  {"SELECT * FROM t LIMIT 10", "SELECT * FROM (SELECT 1 -- note\n) AS baleia_limit LIMIT 5"},
		TSQL:   {"SELECT TOP (10) * FROM t", "SELECT TOP (5) * FROM (SELECT 1 -- note\n) AS baleia_limit"},
		Oracle: {"SELECT * FROM t FETCH FIRST 10 ROWS ONLY", "SELECT * FROM (SELECT 1 -- note\n) FETCH FIRST 5 ROWS ONLY"},
		MySQL:  {"SELECT * FROM t LIMIT 10", "SELECT * FROM (SELECT 1 -- note\n) AS baleia_limit LIMIT 5"},
	}
	for n, w := range want {
		d := testRegistry.MustGet(n)
		if got := d.PreviewSQL("t", 10); got != w[0] {
			t.Errorf("%s preview: %s", n, got)
		}
		if got := d.Limit("SELECT 1 -- note\n ;\n", 5); got != w[1] {
			t.Errorf("%s limit: %s", n, got)
		}
	}
	if _, ok := testRegistry.MustGet(TSQL).ExplainSQL("SELECT 1"); ok {
		t.Error("tsql has no single-statement EXPLAIN")
	}
	if got, ok := testRegistry.MustGet(SQLite).ExplainSQL("SELECT 1;"); !ok || got != "EXPLAIN QUERY PLAN SELECT 1" {
		t.Errorf("sqlite explain: %q", got)
	}
}

// TestDecimal pins decimal()'s ok/err contract. The shape-vs-range hint is
// asserted by message because it is the only observable difference between
// the two malformed-decimal paths; both are ErrInvalid.
func TestDecimal(t *testing.T) {
	for _, typ := range []LogicalType{"", "uuid", "integer", "decimal", "decimals(10,2)", "numeric"} {
		if p, s, ok, err := typ.decimal(); ok || err != nil {
			t.Errorf("%q: (%d,%d,%v,%v), want ok=false, err=nil", typ, p, s, ok, err)
		}
	}
	for _, c := range []struct {
		typ  LogicalType
		p, s int
	}{
		{Decimal(18, 2), 18, 2},
		{LogicalType("decimal(10,2)"), 10, 2},  // typemap spelling, no space
		{LogicalType("decimal(38, 0)"), 38, 0}, // typemap spelling, space
	} {
		p, s, ok, err := c.typ.decimal()
		if !ok || err != nil || p != c.p || s != c.s {
			t.Errorf("%q: (%d,%d,%v,%v), want (%d,%d,true,nil)", c.typ, p, s, ok, err, c.p, c.s)
		}
	}
	for _, typ := range []LogicalType{"decimal(10", "decimal()", "decimal(10,2,3)"} {
		_, _, ok, err := typ.decimal()
		if !ok || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "precision 1-38") {
			t.Errorf("%q: shape error without the range hint, got %v", typ, err)
		}
	}
	for _, typ := range []LogicalType{"decimal(0,1)", "decimal(39,2)", "decimal(10,11)", "decimal(x,2)", "decimal(1,)"} {
		_, _, ok, err := typ.decimal()
		if !ok || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "precision 1-38") {
			t.Errorf("%q: want the range hint, got %v", typ, err)
		}
	}
}

func TestLiteral(t *testing.T) {
	ts := time.Date(2024, 3, 5, 10, 20, 30, 123456000, time.FixedZone("x", 3600))
	cases := []struct {
		dialect string
		v       any
		typ     LogicalType
		want    string
	}{
		{Trino, true, TypeBoolean, "TRUE"},
		{TSQL, true, TypeBoolean, "CAST(1 AS BIT)"},
		{SQLite, false, TypeBoolean, "0"},
		{Trino, int64(42), TypeInt64, "42"},
		{Trino, "12.50", Decimal(18, 2), "CAST(12.50 AS DECIMAL(18,2))"},
		{ClickHouse, "12.50", Decimal(18, 2), "CAST('12.50' AS Decimal(18, 2))"},
		{Trino, 1.5, TypeFloat64, "CAST(1.5 AS DOUBLE)"},
		{PostgreSQL, float64(2), TypeFloat64, "CAST(2.0 AS DOUBLE PRECISION)"},
		{Trino, ts, TypeDate, "DATE '2024-03-05'"},
		{TSQL, "2024-03-05", TypeDate, "CAST('2024-03-05' AS DATE)"},
		{ClickHouse, "2024-03-05", TypeDate, "toDate32('2024-03-05')"},
		{Trino, ts, TypeTimestamp, "TIMESTAMP '2024-03-05 10:20:30.123456'"},
		{Trino, ts, TypeTimestampTZ, "TIMESTAMP '2024-03-05 09:20:30.123456 UTC'"},
		{PostgreSQL, ts, TypeTimestampTZ, "TIMESTAMPTZ '2024-03-05 09:20:30.123456+00'"},
		{TSQL, ts, TypeTimestampTZ, "CAST('2024-03-05 09:20:30.123456 +00:00' AS DATETIMEOFFSET(6))"},
		{Trino, []byte{0xca, 0xfe}, TypeBinary, "X'CAFE'"},
		{PostgreSQL, []byte{0xca, 0xfe}, TypeBinary, "decode('CAFE', 'hex')"},
		{TSQL, []byte{0xca, 0xfe}, TypeBinary, "0xCAFE"},
		{Oracle, []byte{0xca, 0xfe}, TypeBinary, "HEXTORAW('CAFE')"},
		{Trino, `{"a":1}`, TypeJSON, `JSON '{"a":1}'`},
		{MySQL, `{"a":"\\"}`, TypeJSON, `CAST('{"a":"\\\\"}' AS JSON)`},
		{TSQL, "x", TypeString, "N'x'"},
		{Trino, nil, TypeString, "NULL"},
	}
	for _, c := range cases {
		got, err := testRegistry.MustGet(c.dialect).Literal(c.v, c.typ)
		if err != nil || got != c.want {
			t.Errorf("%s %s %v: %q, %v (want %q)", c.dialect, c.typ, c.v, got, err, c.want)
		}
	}
	bad := []struct {
		dialect string
		v       any
		typ     LogicalType
		err     error
	}{
		{Trino, "1;DROP", Decimal(10, 2), ErrInvalid},
		{Trino, "2024-3-5", TypeDate, ErrInvalid},
		{Trino, `{"a":`, TypeJSON, ErrInvalid},
		{Trino, 1, TypeBoolean, ErrInvalid},
		{Trino, "1", Decimal(40, 2), ErrInvalid},
		{MySQL, ts, TypeTimestampTZ, ErrUnsupported},
		{Trino, 1.0, "uuid", ErrInvalid},
	}
	for _, c := range bad {
		if _, err := testRegistry.MustGet(c.dialect).Literal(c.v, c.typ); !errors.Is(err, c.err) {
			t.Errorf("%s %s %v: %v, want %v", c.dialect, c.typ, c.v, err, c.err)
		}
	}
}

// TestGenerationGolden fixes the SQL each dialect generates for the
// semantic-layer primitives. The integration test runs the same statements
// against real servers (#14 PR 3).
func TestGenerationGolden(t *testing.T) {
	type golden struct{ approx, month, week, cast, divide string }
	want := map[string]golden{
		Trino: {"approx_distinct(x)", "date_trunc('month', ts)", "date_trunc('week', ts)",
			"CAST(x AS DECIMAL(18,2))", "(CAST(a AS DOUBLE)) / NULLIF(b, 0)"},
		PostgreSQL: {"COUNT(DISTINCT x)", "date_trunc('month', ts)", "date_trunc('week', ts)",
			"CAST(x AS NUMERIC(18,2))", "(CAST(a AS DOUBLE PRECISION)) / NULLIF(b, 0)"},
		MySQL: {"COUNT(DISTINCT x)", "CAST(DATE_FORMAT(ts, '%Y-%m-01 00:00:00') AS DATETIME)",
			"CAST(DATE_SUB(DATE(ts), INTERVAL WEEKDAY(ts) DAY) AS DATETIME)",
			"CAST(x AS DECIMAL(18,2))", "(a) / NULLIF(b, 0)"},
		MariaDB: {"COUNT(DISTINCT x)", "CAST(DATE_FORMAT(ts, '%Y-%m-01 00:00:00') AS DATETIME)",
			"CAST(DATE_SUB(DATE(ts), INTERVAL WEEKDAY(ts) DAY) AS DATETIME)",
			"CAST(x AS DECIMAL(18,2))", "(a) / NULLIF(b, 0)"},
		TSQL: {"APPROX_COUNT_DISTINCT(x)", "DATETRUNC(month, ts)", "DATETRUNC(iso_week, ts)",
			"CAST(x AS DECIMAL(18,2))", "(CAST(a AS FLOAT)) / NULLIF(b, 0)"},
		Oracle: {"APPROX_COUNT_DISTINCT(x)", "TRUNC(ts, 'MM')", "TRUNC(ts, 'IW')",
			"CAST(x AS NUMBER(18,2))", "(a) / NULLIF(b, 0)"},
		SQLite: {"COUNT(DISTINCT x)", "datetime(ts, 'start of month')", "datetime(ts, 'weekday 0', '-6 days', 'start of day')",
			"CAST(x AS NUMERIC)", "(CAST(a AS REAL)) / NULLIF(b, 0)"},
		ClickHouse: {"uniq(x)", "date_trunc('month', ts)", "date_trunc('week', ts)",
			"CAST(x AS Decimal(18, 2))", "(a) / NULLIF(b, 0)"},
		FlightSQL: {"COUNT(DISTINCT x)", "date_trunc('month', ts)", "date_trunc('week', ts)",
			"CAST(x AS DECIMAL(18,2))", "(CAST(a AS DOUBLE PRECISION)) / NULLIF(b, 0)"},
	}
	for _, n := range allDialects {
		d, w := testRegistry.MustGet(n), want[n]
		approx, err1 := d.Aggregate(AggApproxCountDistinct, "x", false)
		month, err2 := d.TimeTrunc("ts", GrainMonth)
		week, err3 := d.TimeTrunc("ts", GrainWeek)
		cast, err4 := d.Cast("x", Decimal(18, 2))
		got := golden{approx, month, week, cast, d.SafeDivide("a", "b")}
		if err := errors.Join(err1, err2, err3, err4); err != nil || got != w {
			t.Errorf("%s:\n got %+v\nwant %+v (%v)", n, got, w, err)
		}
		for _, g := range Grains {
			if _, err := d.TimeTrunc("ts", g); err != nil {
				t.Errorf("%s: grain %s: %v", n, g, err)
			}
		}
		if s, err := d.Aggregate(AggSum, "x", true); err != nil || s != "SUM(DISTINCT x)" {
			t.Errorf("%s: sum distinct %q, %v", n, s, err)
		}
		if s, _ := d.Aggregate(AggCount, "", false); s != "COUNT(*)" {
			t.Errorf("%s: count(*) %q", n, s)
		}
	}
	if _, err := testRegistry.MustGet(Trino).TimeTrunc("ts", "fortnight"); !errors.Is(err, ErrInvalid) {
		t.Error("unknown grain accepted")
	}
	if _, err := testRegistry.MustGet(MySQL).Cast("x", TypeBoolean); !errors.Is(err, ErrUnsupported) {
		t.Error("mysql cast to boolean must be unsupported")
	}
	if _, err := testRegistry.MustGet(MariaDB).Cast("x", TypeJSON); !errors.Is(err, ErrUnsupported) {
		t.Error("mariadb cast to json must be unsupported")
	}
	if got, _ := testRegistry.MustGet(SQLite).Cast("x", TypeTimestamp); got != "datetime(x)" {
		t.Errorf("sqlite timestamp cast: %s", got)
	}
}

// TestSplitStatements_trino keeps the cases of the former sqlsplitter suite.
func TestSplitStatements_trino(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT 1", []string{"SELECT 1"}},
		{"SELECT 1; SELECT 2; SELECT 3", []string{"SELECT 1", "SELECT 2", "SELECT 3"}},
		{";;SELECT 1;;", []string{"SELECT 1"}},
		{"SELECT 'hello; world'", []string{"SELECT 'hello; world'"}},
		{"SELECT 'it''s fine'; SELECT 2", []string{"SELECT 'it''s fine'", "SELECT 2"}},
		{`SELECT * FROM "my;table"; SELECT 2`, []string{`SELECT * FROM "my;table"`, "SELECT 2"}},
		{"SELECT 1 -- ignore this ; semicolon\n; SELECT 2", []string{"SELECT 1 -- ignore this ; semicolon", "SELECT 2"}},
		{"SELECT 1 /* ignore ; this */ ; SELECT 2", []string{"SELECT 1 /* ignore ; this */", "SELECT 2"}},
		{"SELECT 1;", []string{"SELECT 1"}},
		{"   \n\t  ", nil},
		{"\nCREATE TABLE foo (\n    id INT,\n    name VARCHAR\n);\n\nINSERT INTO foo VALUES (1, 'hello');\n", []string{"CREATE TABLE foo (\n    id INT,\n    name VARCHAR\n)", "INSERT INTO foo VALUES (1, 'hello')"}},
	}
	for _, c := range cases {
		got := split(t, Trino, c.sql)
		if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("%q: %q, want %q", c.sql, got, c.want)
		}
	}
	// Dollar quoting is PostgreSQL's (the old splitter applied it to Trino).
	if got := split(t, PostgreSQL, "SELECT $$hello; world$$; SELECT 2"); len(got) != 2 {
		t.Errorf("postgresql $$: %q", got)
	}
}

// Trino quoting cases moved from internal/trino when its package-level
// helpers went away (#364 follow-up).
func TestTrino_QuoteIdent(t *testing.T) {
	trino := NewRegistry().MustGet(Trino)
	for in, want := range map[string]string{
		"orders":     `"orders"`,
		`a"b`:        `"a""b"`,
		"x; DROP":    `"x; DROP"`,
		"with space": `"with space"`,
		`"`:          `""""`,
		"ünïcode":    `"ünïcode"`,
		"new\nline":  "\"new\nline\"",
		"semi;--/*":  `"semi;--/*"`,
	} {
		got, err := trino.QuoteIdent(in)
		if err != nil || got != want {
			t.Errorf("QuoteIdent(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a\x00b"} {
		if _, err := trino.QuoteIdent(bad); err == nil {
			t.Errorf("QuoteIdent(%q) accepted", bad)
		}
	}
}

func TestTrino_QualifiedName(t *testing.T) {
	trino := NewRegistry().MustGet(Trino)
	got, err := trino.QualifiedName("cat", `we"ird`, "t")
	if err != nil || got != `"cat"."we""ird"."t"` {
		t.Fatalf("QualifiedName = %q, %v", got, err)
	}
	if _, err := trino.QualifiedName("cat", "", "t"); err == nil {
		t.Fatal("an empty part must fail the whole name")
	}
}

func TestTrino_QuoteString(t *testing.T) {
	trino := NewRegistry().MustGet(Trino)
	for in, want := range map[string]string{"7d": `'7d'`, "it's": `'it''s'`, `'; DROP`: `'''; DROP'`} {
		if got := trino.QuoteString(in); got != want {
			t.Errorf("QuoteString(%q) = %q, want %q", in, got, want)
		}
	}
}

// testRegistry is the registry the package's own tests read dialects from.
var testRegistry = NewRegistry()

// TestForEngineKindDeprecated: the package-level lookup QueryExecutionWorkflowV2
// still uses gives the same answers as the registry; it goes with V2.
// TestForEngineKind covers the kind → dialect translation: every engine kind
// the API can carry resolves, and a miss is (nil, false) explicitly rather
// than through a lookup of the empty name.
func TestForEngineKindDeprecated(t *testing.T) {
	for kind, want := range map[string]string{
		"trino": Trino, "postgresql": PostgreSQL, "mysql": MySQL, "mariadb": MariaDB,
		"sqlserver": TSQL, "oracle": Oracle, "sqlite": SQLite, "clickhouse": ClickHouse,
		"flightsql": FlightSQL, "fake": FlightSQL, // the test fake shares the ANSI subset
	} {
		d, ok := ForEngineKind(kind) //nolint:staticcheck // the deprecated lookup is under test
		if !ok || d == nil || d.Name() != want {
			t.Errorf("ForEngineKind(%q) = (%v, %v), want %q", kind, d, ok, want)
		}
	}
	for _, kind := range []string{"", "nope", "postgres", "sql-server"} {
		if d, ok := ForEngineKind(kind); ok || d != nil { //nolint:staticcheck // the deprecated lookup is under test
			t.Errorf("ForEngineKind(%q) = (%v, %v), want (nil, false)", kind, d, ok)
		}
	}
}
