package policy

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestIsCatalogDDL(t *testing.T) {
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
			if got := IsCatalogDDL(tt.sql); got != tt.want {
				t.Errorf("IsCatalogDDL(%q) = %v; want %v", tt.sql, got, tt.want)
			}
		})
	}
}

func TestStatementForbidden(t *testing.T) {
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
		{"execute immediate plain select", "EXECUTE IMMEDIATE 'SELECT 1'", false},
		{"execute immediate no literal", "EXECUTE IMMEDIATE", false},
		{"create schema", "CREATE SCHEMA s", false},
		{"create table", "CREATE TABLE t (a int)", false},
		{"set session property", "SET SESSION query_max_execution_time = '30m'", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statementForbidden(tt.sql); got != tt.want {
				t.Errorf("statementForbidden(%q) = %v; want %v", tt.sql, got, tt.want)
			}
		})
	}
}

// TestForbiddenInEditor: catalog DDL is forbidden on every engine, whatever
// the engine supports, including when hidden after another statement with
// that engine's own separators.
func TestForbiddenInEditor(t *testing.T) {
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
