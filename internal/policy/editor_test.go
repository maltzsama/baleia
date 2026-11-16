package policy

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestIsCatalogDDL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		// Positive cases — must be detected.
		{"plain create", "CREATE CATALOG baleia_polaris", true},
		{"plain drop", "DROP CATALOG baleia_polaris", true},
		{"lowercase", "create catalog baleia_polaris", true},
		{"mixed case", "CrEaTe CatAlOg baleia_polaris", true},
		{"leading spaces", "   CREATE CATALOG baleia_polaris", true},
		{"tab newline leading", "\t\n\tCREATE CATALOG baleia_polaris", true},
		{"extra spaces between", "CREATE   CATALOG   baleia_polaris", true},
		{"newline between", "CREATE\nCATALOG\nbaleia_polaris", true},
		{"with WITH clause", `CREATE CATALOG baleia_polaris USING iceberg WITH ("x" = 'y')`, true},
		{"IF EXISTS", "DROP CATALOG IF EXISTS baleia_polaris", true},
		{"IF NOT EXISTS", "CREATE CATALOG IF NOT EXISTS x USING tpch", true},
		{"semicolon suffix", "CREATE CATALOG baleia_polaris;", true},

		// Comment smuggling must still be caught.
		{"line comment prefix", "-- hi there\nCREATE CATALOG baleia_polaris", true},
		{"block comment prefix", "/* hi */ CREATE CATALOG baleia_polaris", true},
		{"multi line comment", "-- one\n-- two\nCREATE CATALOG baleia_polaris", true},
		{"block then line comment", "/* a */ -- b\nCREATE CATALOG baleia_polaris", true},

		// Negative cases — must not be flagged.
		{"plain select", "SELECT * FROM analytics.events", false},
		{"show catalogs", "SHOW CATALOGS", false},
		{"create schema", "CREATE SCHEMA baleia", false},
		{"create table", "CREATE TABLE x (a int)", false},
		{"create view", "CREATE VIEW x AS SELECT 1", false},
		{"drop table", "DROP TABLE x", false},
		{"create materialized", "CREATE MATERIALIZED VIEW x AS SELECT 1", false},
		{"empty", "", false},
		{"only whitespace", "   ", false},
		{"comment only", "-- nothing", false},
		{"block comment only", "/* nothing */", false},

		// Not fooled by wording that contains "create catalog" inside.
		{"create table mentioning catalog", "CREATE TABLE catalog_log (x int)", false},
		{"comment about create catalog", "-- I will CREATE CATALOG later\nSELECT 1", false},
		{"string containing CREATE CATALOG", "SELECT 'CREATE CATALOG evil' FROM t", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsCatalogDDL(tt.sql); got != tt.want {
				t.Errorf("IsCatalogDDL(%q) = %v; want %v", tt.sql, got, tt.want)
			}
		})
	}
}

func TestStatementForbidden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{"plain catalog ddl", "CREATE CATALOG x USING tpch", true},
		{"drop catalog ddl", "DROP CATALOG x", true},
		{"set session authorization", "SET SESSION AUTHORIZATION 'baleia_admin'", true},
		{"set session authorization bare", "SET SESSION AUTHORIZATION", true},
		{"set session authorization mixed case", "set  session   authorization   foo", true},
		{"commented set session authorization", "/* c */ SET SESSION AUTHORIZATION a", true},
		{"execute immediate drop catalog", "EXECUTE IMMEDIATE 'drop   catalog x'", true},
		{"execute immediate create catalog", "EXECUTE IMMEDIATE 'CREATE CATALOG x USING tpch'", true},
		{"execute immediate lower", "execute immediate 'DROP CATALOG IF EXISTS y'", true},
		{"execute immediate escape string", "EXECUTE IMMEDIATE E'drop catalog z'", true},
		{"execute immediate with escaped quote", "EXECUTE IMMEDIATE 'drop catalog  we''re'", true},
		{"execute immediate with comments in literal", "EXECUTE IMMEDIATE '-- c\ndrop catalog q'", true},

		{"plain select", "SELECT * FROM t", false},
		{"select string containing create catalog", "SELECT 'create catalog' FROM t", false},
		{"select containing set session string", "SELECT 'SET SESSION AUTHORIZATION'", false},
		{"execute immediate plain select", "EXECUTE IMMEDIATE 'SELECT 1'", true},
		{"execute immediate no literal", "EXECUTE IMMEDIATE", true},
		{"create schema", "CREATE SCHEMA s", false},
		{"create table", "CREATE TABLE t (a int)", false},
		{"set session property", "SET SESSION query_max_execution_time = '30m'", false},
		{"show tables", "SHOW TABLES", false},
		{"explain", "EXPLAIN SELECT 1", false},
		{"call", "CALL foo()", true},
		{"vacuum", "VACUUM t", true},
		{"optimize", "OPTIMIZE TABLE t", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := statementForbiddenWithDialect(genericDialect, tt.sql); got != tt.want {
				t.Errorf("statementForbidden(%q) = %v; want %v", tt.sql, got, tt.want)
			}
		})
	}
}

// TestForbiddenInEditor: catalog DDL is forbidden on every engine, whatever
// the engine supports, including when hidden after another statement with
// that engine's own separators.
func TestForbiddenInEditor(t *testing.T) {
	t.Parallel()
	for _, name := range dialect.NewRegistry().Names() {
		d := dialect.NewRegistry().MustGet(name)
		for _, sql := range []string{
			"CREATE CATALOG x USING tpch",
			"SELECT 1; DROP CATALOG x",
			"SELECT 1;\n-- hi\n/* c */ create   catalog y using tpch",
		} {
			if !ForbiddenInEditor(d, sql) {
				t.Errorf("%s: %q allowed", name, sql)
			}
			if _, err := EditorStatements(d, sql); !errors.Is(err, ErrForbiddenInEditor) {
				t.Errorf("%s: EditorStatements(%q) = %v", name, sql, err)
			}
		}
		if ForbiddenInEditor(d, "SELECT 'drop catalog x'; SELECT 2") {
			t.Errorf("%s: a string literal was judged as a statement", name)
		}
		stmts, err := EditorStatements(d, "SELECT 1; SELECT 2")
		if err != nil || len(stmts) != 2 {
			t.Errorf("%s: %q, %v", name, stmts, err)
		}
	}
	tsql := dialect.NewRegistry().MustGet(dialect.TSQL)
	if !ForbiddenInEditor(tsql, "SELECT 1\nGO\nDROP CATALOG x") {
		t.Error("tsql: catalog DDL after GO allowed")
	}
	// An unsplittable script is judged whole, and EditorStatements reports why.
	if !ForbiddenInEditor(dialect.NewRegistry().MustGet(dialect.Trino), "DROP CATALOG x; SELECT 'open") {
		t.Error("unterminated script hid catalog DDL")
	}
	if _, err := EditorStatements(dialect.NewRegistry().MustGet(dialect.Trino), "SELECT 'open"); !errors.Is(err, dialect.ErrInvalid) {
		t.Errorf("unterminated string: %v", err)
	}
}

// TestSetSessionAuthorizationAcrossDialects: SET SESSION AUTHORIZATION is a
// Command on most engines but a structured Set on PostgreSQL; the gate must
// reject it on every engine without rejecting ordinary SET.
func TestSetSessionAuthorizationAcrossDialects(t *testing.T) {
	t.Parallel()
	for _, name := range dialect.NewRegistry().Names() {
		d := dialect.NewRegistry().MustGet(name)
		for _, sql := range []string{
			"SET SESSION AUTHORIZATION 'baleia_admin'",
			"SET SESSION AUTHORIZATION",
			"set  session   authorization   foo",
			"/* c */ SET SESSION AUTHORIZATION a",
			"SET SESSION SESSION AUTHORIZATION 'baleia_admin'",
			"SET /* c */ SESSION AUTHORIZATION 'baleia_admin'",
		} {
			if !ForbiddenInEditor(d, sql) {
				t.Errorf("%s: %q allowed", name, sql)
			}
		}
		for _, sql := range []string{
			"SET x = 1",
			"SET SESSION query_max_execution_time = '30m'",
			"SET ROLE foo",
		} {
			if ForbiddenInEditor(d, sql) {
				t.Errorf("%s: %q forbidden", name, sql)
			}
		}
	}
}

// TestExecuteImmediateAcrossDialects: dynamic SQL that smuggles catalog DDL is
// rejected whether it parses as a Command (most engines) or an Execute (T-SQL);
// a stored-procedure call is not dynamic SQL.
func TestExecuteImmediateAcrossDialects(t *testing.T) {
	t.Parallel()
	tsql := dialect.NewRegistry().MustGet(dialect.TSQL)
	if !ForbiddenInEditor(tsql, "EXECUTE IMMEDIATE 'CREATE CATALOG x USING tpch'") {
		t.Error("tsql: EXECUTE IMMEDIATE smuggling catalog DDL allowed")
	}
	if ForbiddenInEditor(tsql, "EXECUTE my_proc") {
		t.Error("tsql: EXECUTE my_proc judged dynamic SQL")
	}
	if ForbiddenInEditor(tsql, "EXEC sp_help") {
		t.Error("tsql: EXEC sp_help judged dynamic SQL")
	}
}

// TestFailClosedOnParseError: a statement the parser cannot read is rejected
// rather than assumed safe.
func TestFailClosedOnParseError(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	for _, sql := range []string{"SELECT FROM (((", "'''", "SELECT 'open"} {
		if !ForbiddenInEditor(d, sql) {
			t.Errorf("unparseable %q allowed", sql)
		}
	}
}

// TestOracleAnonymousBlock: dynamic SQL hidden in an anonymous block body is
// rejected, while a bare BEGIN (a T-SQL transaction) and a block without
// dynamic SQL are allowed.
func TestOracleAnonymousBlock(t *testing.T) {
	t.Parallel()
	oracle := dialect.NewRegistry().MustGet(dialect.Oracle)
	if !ForbiddenInEditor(oracle, "BEGIN EXECUTE IMMEDIATE 'DROP TABLE t'; END;") {
		t.Error("oracle: anonymous block hiding EXECUTE IMMEDIATE allowed")
	}
	if ForbiddenInEditor(oracle, "BEGIN NULL; END;") {
		t.Error("oracle: a plain anonymous block was forbidden")
	}
	if ForbiddenInEditor(dialect.NewRegistry().MustGet(dialect.TSQL), "BEGIN") {
		t.Error("tsql: a bare BEGIN (transaction) was forbidden")
	}
}

// TestEditorAllowList: the gate is an allow-list, not a deny-list. Read-only
// introspection is allowed even where the fork leaves it an opaque Command;
// unrecognized and maintenance commands are rejected on every engine.
func TestEditorAllowList(t *testing.T) {
	t.Parallel()
	for _, name := range dialect.NewRegistry().Names() {
		d := dialect.NewRegistry().MustGet(name)
		for _, sql := range []string{
			"SHOW TABLES",
			"EXPLAIN SELECT 1",
			"SELECT 1",
			"INSERT INTO t VALUES (1)",
			"CREATE TABLE t (a int)",
		} {
			if ForbiddenInEditor(d, sql) {
				t.Errorf("%s: %q forbidden", name, sql)
			}
		}
		for _, sql := range []string{
			"CALL foo()",
			"VACUUM t",
			"OPTIMIZE TABLE t",
			"CREATE CATALOG x USING tpch",
		} {
			if !ForbiddenInEditor(d, sql) {
				t.Errorf("%s: %q allowed", name, sql)
			}
		}
	}
}
