package sqlsplitter

import "testing"

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