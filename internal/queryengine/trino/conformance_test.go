package trino

import (
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
)

// TestConformance runs the engine suite against a real coordinator with the
// catalog guard installed (the compose Trino). Set BALEIA_TEST_TRINO_URL.
func TestConformance(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_TRINO_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_TRINO_URL not set")
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	conformance.Run(t, func(*testing.T) queryengine.Engine {
		return New("conformance", url, "baleia", "baleia_admin", d, dialect.NewRegistry().MustGet(dialect.Trino))
	}, conformance.Fixtures{
		TypesSQL: `SELECT true AS "bool", BIGINT '9007199254740993' AS "int64",
			CAST('1234567890123456789012345678.0123456789' AS DECIMAL(38,10)) AS "decimal",
			DATE '2024-02-29' AS "date",
			TIMESTAMP '2024-02-29 13:45:30.123456' AS "timestamp",
			TIMESTAMP '2024-02-29 13:45:30.123456 UTC' AS "timestamptz",
			'ação 漢字 🐳' AS "varchar", NULL AS "null", X'000102FF' AS "binary"`,
		Types: []conformance.TypeCase{
			{Column: "bool", Type: arrow.FixedWidthTypes.Boolean, Value: "true"},
			{Column: "int64", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "date", Type: arrow.FixedWidthTypes.Date32, Value: "2024-02-29"},
			{Column: "timestamp", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: "2024-02-29T13:45:30.123456"},
			{Column: "timestamptz", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "varchar", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "null", Type: arrow.Null, Value: "(null)"},
			{Column: "binary", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
		},
		ZeroRowsSQL:  "SELECT 1 AS n WHERE false",
		SetStateSQL:  "USE system.runtime",
		ReadStateSQL: "SELECT current_schema",
		StateValue:   "runtime",
		ReadSQL:      "SELECT 1",
		// The guard's read-only mode denies every write and DDL check.
		WriteSQL: "CREATE TABLE system.runtime.baleia_conformance (a integer)",
		SleepSQL: `SELECT count(*) FROM UNNEST(sequence(1, 10000)) a(x)
			CROSS JOIN UNNEST(sequence(1, 10000)) b(y) CROSS JOIN UNNEST(sequence(1, 10000)) c(z)`,
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM system.runtime.baleia_missing",
		ManyRowsSQL:       "SELECT x FROM UNNEST(sequence(1, 5000)) t(x)",
		CollectCap:        1000,
		IntrospectCatalog: "system",
		IntrospectSchema:  []string{"runtime"},
		IntrospectTable:   "nodes",
		IntrospectColumns: []string{"node_id", "http_uri", "node_version", "coordinator", "state"},
	})
}
