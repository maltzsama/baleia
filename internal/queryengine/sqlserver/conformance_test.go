package sqlserver

import (
	"context"
	"database/sql"
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	_ "github.com/microsoft/go-mssqldb"
)

const confDB = "baleia_conformance"

// TestConformance runs the engine suite against a real SQL Server. Set
// BALEIA_TEST_SQLSERVER_URL (sqlserver://sa:pass@host:port).
func TestConformance(t *testing.T) {
	t.Parallel()
	raw := os.Getenv("BALEIA_TEST_SQLSERVER_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_SQLSERVER_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	setup(t, raw)

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := connections.Config{
		Kind: connections.KindSQLServer,
		Values: map[string]string{
			"host": host, "port": port, "username": u.User.Username(),
			"database": confDB, "encrypt": "disable",
		},
		Secrets: map[string]string{"password": pass},
	}
	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		e, err := newEngine(dialect.NewRegistry().MustGet(dialect.TSQL))(context.Background(), queryengine.EngineRow{ID: "conformance", Kind: Kind}, cfg, d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}, conformance.Fixtures{
		TypesSQL: `SELECT c_bit, c_tinyint, c_smallint, c_int, c_bigint, c_decimal, c_money, c_smallmoney,
			c_float, c_real, c_char, c_varchar, c_nchar, c_nvarchar, c_varbinary,
			c_date, c_datetime2, c_dto, c_time, c_uid, c_null
			FROM dbo.baleia_conformance_types`,
		Types: []conformance.TypeCase{
			{Column: "c_bit", Type: arrow.FixedWidthTypes.Boolean, Value: "true"},
			{Column: "c_tinyint", Type: arrow.PrimitiveTypes.Uint8, Value: "200"},
			{Column: "c_smallint", Type: arrow.PrimitiveTypes.Int16, Value: "100"},
			{Column: "c_int", Type: arrow.PrimitiveTypes.Int32, Value: "100000"},
			{Column: "c_bigint", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "c_decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "c_money", Type: &arrow.Decimal128Type{Precision: 19, Scale: 4}, Value: "1234.5678"},
			{Column: "c_smallmoney", Type: &arrow.Decimal128Type{Precision: 10, Scale: 4}, Value: "12.3456"},
			{Column: "c_float", Type: arrow.PrimitiveTypes.Float64, Value: "1.5"},
			{Column: "c_real", Type: arrow.PrimitiveTypes.Float32, Value: "2.5"},
			{Column: "c_char", Type: arrow.BinaryTypes.String, Value: "abc"},
			{Column: "c_varchar", Type: arrow.BinaryTypes.String, Value: "hello"},
			{Column: "c_nchar", Type: arrow.BinaryTypes.String, Value: "abc"},
			{Column: "c_nvarchar", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "c_varbinary", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
			{Column: "c_date", Type: arrow.FixedWidthTypes.Date32, Value: "2024-02-29"},
			{Column: "c_datetime2", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: "2024-02-29T13:45:30.123456"},
			{Column: "c_dto", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "c_time", Type: &arrow.Time64Type{Unit: arrow.Nanosecond}, Value: "13:45:30.123456700"},
			{Column: "c_uid", Type: arrow.BinaryTypes.String, Value: "11111111-2222-3333-4444-555555555555"},
			{Column: "c_null", Type: arrow.BinaryTypes.String, Value: "(null)"},
		},
		ZeroRowsSQL:       "SELECT 1 AS n WHERE 1 = 0",
		MultiResultSQL:    "SELECT 1 AS a; SELECT 2 AS b",
		SetStateSQL:       "SET DATEFIRST 3",
		ReadStateSQL:      "SELECT @@DATEFIRST",
		StateValue:        "3",
		SleepSQL:          "WAITFOR DELAY '00:01:00'",
		SyntaxErrorSQL:    "SELECT * FROM",
		MissingTableSQL:   "SELECT * FROM dbo.baleia_missing_conformance",
		ManyRowsSQL:       "WITH seq AS (SELECT 1 n UNION ALL SELECT n + 1 FROM seq WHERE n < 5000) SELECT n FROM seq OPTION (MAXRECURSION 0)",
		CollectCap:        1000,
		BeginSQL:          "BEGIN TRANSACTION",
		InsertSQL:         "INSERT INTO dbo.baleia_conformance_tx (n) VALUES (1)",
		CountSQL:          "SELECT COUNT(*) FROM dbo.baleia_conformance_tx",
		IntrospectCatalog: confDB,
		IntrospectSchema:  []string{"dbo"},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// setup creates the database and fixture tables.
func setup(t *testing.T, raw string) {
	t.Helper()
	ctx := context.Background()
	admin, err := sql.Open("sqlserver", raw+"?encrypt=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	t.Cleanup(func() {
		_, _ = admin.Exec("ALTER DATABASE " + confDB + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE " + confDB)
	})
	for _, stmt := range []string{
		`IF DB_ID('` + confDB + `') IS NOT NULL BEGIN ALTER DATABASE ` + confDB + ` SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ` + confDB + ` END`,
		`CREATE DATABASE ` + confDB,
		`USE ` + confDB + `; CREATE TABLE dbo.baleia_conformance_types (
			c_bit BIT, c_tinyint TINYINT, c_smallint SMALLINT, c_int INT, c_bigint BIGINT,
			c_decimal DECIMAL(38,10), c_money MONEY, c_smallmoney SMALLMONEY, c_float FLOAT, c_real REAL,
			c_char CHAR(3), c_varchar VARCHAR(20), c_nchar NCHAR(3), c_nvarchar NVARCHAR(20),
			c_varbinary VARBINARY(4), c_date DATE, c_datetime2 DATETIME2(6), c_dto DATETIMEOFFSET(6),
			c_time TIME(7), c_uid UNIQUEIDENTIFIER, c_null NVARCHAR(10)
		)`,
		`USE ` + confDB + `; INSERT INTO dbo.baleia_conformance_types VALUES (
			1, 200, 100, 100000, 9007199254740993,
			1234567890123456789012345678.0123456789, 1234.5678, 12.3456, 1.5, 2.5,
			'abc', 'hello', 'abc', N'ação 漢字 🐳',
			0x000102ff, '2024-02-29', '2024-02-29 13:45:30.123456', '2024-02-29 13:45:30.123456 +00:00',
			'13:45:30.1234567', '11111111-2222-3333-4444-555555555555', NULL
		)`,
		`USE ` + confDB + `; CREATE TABLE dbo.baleia_conformance_tx (n bigint)`,
		`USE ` + confDB + `; CREATE TABLE dbo.baleia_conformance_table (id bigint NOT NULL, name nvarchar(20))`,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}
