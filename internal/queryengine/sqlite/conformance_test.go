package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
)

// TestConformance runs the engine suite against a writable SQLite database in
// a temporary root. The suite needs writes for the transaction case, so writes
// are enabled; the read-only case still exercises the read-only pool.
func TestConformance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "conformance.db")
	setupConformance(t, path)

	o := Options{Root: dir, AllowWrite: true}
	cfg := connections.Config{
		Kind:   connections.KindSQLite,
		Values: map[string]string{"path": "conformance.db", "read_write": "true"},
	}
	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		e, err := newEngineWithOptions(queryengine.EngineRow{ID: "conformance", Kind: Kind}, cfg, o, testDialect)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}, conformance.Fixtures{
		TypesSQL: `SELECT c_int, c_real, c_text, c_blob, c_num, c_null FROM baleia_conformance_types`,
		Types: []conformance.TypeCase{
			{Column: "c_int", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "c_real", Type: arrow.PrimitiveTypes.Float64, Value: "1.5"},
			{Column: "c_text", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "c_blob", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
			// NUMERIC has no Arrow mapping: exact text.
			{Column: "c_num", Type: arrow.BinaryTypes.String, Value: "42"},
			{Column: "c_null", Type: arrow.BinaryTypes.String, Value: "(null)"},
		},
		ZeroRowsSQL:       "SELECT c_int AS n FROM baleia_conformance_types WHERE 0",
		SetStateSQL:       "PRAGMA foreign_keys = ON",
		ReadStateSQL:      "PRAGMA foreign_keys",
		StateValue:        "1",
		ReadSQL:           "SELECT 1",
		WriteSQL:          "CREATE TABLE baleia_conformance_ro (a INTEGER)",
		SleepSQL:          "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 1000000000) SELECT count(*) FROM c",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 5000) SELECT x FROM c",
		CollectCap:        1000,
		BeginSQL:          "BEGIN",
		InsertSQL:         "INSERT INTO baleia_conformance_tx (n) VALUES (1)",
		CountSQL:          "SELECT count(*) FROM baleia_conformance_tx",
		IntrospectCatalog: Catalog,
		IntrospectSchema:  []string{},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// setupConformance creates the fixture database the suite uses. mode=rwc is
// needed to create the file; the engine later opens it with mode=rw.
func setupConformance(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE baleia_conformance_types (
			c_int INTEGER, c_real REAL, c_text TEXT, c_blob BLOB, c_num NUMERIC, c_null TEXT)`,
		`INSERT INTO baleia_conformance_types VALUES (
			9007199254740993, 1.5, 'ação 漢字 🐳', X'000102ff', 42, NULL)`,
		`CREATE TABLE baleia_conformance_tx (n INTEGER)`,
		`CREATE TABLE baleia_conformance_table (id INTEGER NOT NULL, name TEXT)`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}
