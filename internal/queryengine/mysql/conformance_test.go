package mysql

import (
	"context"
	"database/sql"

	"net"
	"net/url"
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	driver "github.com/go-sql-driver/mysql"
)

// confDB is the database the conformance fixtures live in.
const confDB = "baleia_conformance"

// TestConformanceMySQL runs the engine suite against a real MySQL server.
// Set BALEIA_TEST_MYSQL_URL.
func TestConformance(t *testing.T) { runConformance(t, "BALEIA_TEST_MYSQL_URL", false) }

// TestConformanceMariaDB is the same suite against MariaDB
// (BALEIA_TEST_MARIADB_URL).
func TestConformanceMariaDB(t *testing.T) { runConformance(t, "BALEIA_TEST_MARIADB_URL", true) }

func runConformance(t *testing.T, env string, mariadb bool) {
	raw := os.Getenv(env)
	if raw == "" {
		t.Skip(env + " not set")
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
	setup(t, u)

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	kind := KindMySQL
	if mariadb {
		kind = KindMariaDB
	}
	cfg := connections.Config{
		Kind: connections.Kind(kind),
		Values: map[string]string{
			"host": host, "port": port, "username": u.User.Username(),
			"database": confDB, "tls": "false",
		},
		Secrets: map[string]string{"password": pass},
	}
	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		e, err := newEngine(mariadb)(context.Background(), queryengine.EngineRow{ID: "conformance", Kind: kind}, cfg, d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}, conformance.Fixtures{
		TypesSQL: `SELECT c_int8, c_int16, c_int32, c_int64, c_uint64, c_decimal, c_float, c_double,
			c_varchar, c_date, c_datetime, c_timestamp, c_binary, c_null
			FROM baleia_conformance_types`,
		Types: []conformance.TypeCase{
			{Column: "c_int8", Type: arrow.PrimitiveTypes.Int8, Value: "-5"},
			{Column: "c_int16", Type: arrow.PrimitiveTypes.Int16, Value: "100"},
			{Column: "c_int32", Type: arrow.PrimitiveTypes.Int32, Value: "100000"},
			{Column: "c_int64", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "c_uint64", Type: arrow.PrimitiveTypes.Uint64, Value: "18446744073709551615"},
			{Column: "c_decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "c_float", Type: arrow.PrimitiveTypes.Float32, Value: "1.5"},
			{Column: "c_double", Type: arrow.PrimitiveTypes.Float64, Value: "2.5"},
			{Column: "c_varchar", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "c_date", Type: arrow.FixedWidthTypes.Date32, Value: "2024-02-29"},
			{Column: "c_datetime", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: "2024-02-29T13:45:30.123456"},
			{Column: "c_timestamp", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "c_binary", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
			{Column: "c_null", Type: arrow.BinaryTypes.String, Value: "(null)"},
		},
		ZeroRowsSQL:       "SELECT c_int32 AS n FROM baleia_conformance_types WHERE 1 = 0",
		SetStateSQL:       "SET SESSION time_zone = '+03:00'",
		ReadStateSQL:      "SELECT @@session.time_zone",
		StateValue:        "+03:00",
		SleepSQL:          "SELECT COUNT(*) FROM information_schema.columns a, information_schema.columns b, information_schema.columns c",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 5000) SELECT n FROM seq",
		CollectCap:        1000,
		BeginSQL:          "START TRANSACTION",
		InsertSQL:         "INSERT INTO baleia_conformance_tx (n) VALUES (1)",
		CountSQL:          "SELECT COUNT(*) FROM baleia_conformance_tx",
		IntrospectCatalog: Catalog,
		IntrospectSchema:  []string{confDB},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// setup creates the database and fixture tables.
func setup(t *testing.T, u *url.URL) {
	t.Helper()
	pass, _ := u.User.Password()
	mc := driver.NewConfig()
	mc.Net, mc.Addr, mc.User, mc.Passwd = "tcp", u.Host, u.User.Username(), pass
	admin, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + confDB) })
	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + confDB,
		"CREATE DATABASE " + confDB,
		`CREATE TABLE ` + confDB + `.baleia_conformance_types (
			c_int8 TINYINT, c_int16 SMALLINT, c_int32 INT, c_int64 BIGINT, c_uint64 BIGINT UNSIGNED,
			c_decimal DECIMAL(38,10), c_float FLOAT, c_double DOUBLE, c_varchar VARCHAR(50),
			c_date DATE, c_datetime DATETIME(6), c_timestamp TIMESTAMP(6) NULL,
			c_binary VARBINARY(4), c_null VARCHAR(10)
		) ENGINE=InnoDB`,
		`INSERT INTO ` + confDB + `.baleia_conformance_types VALUES (
			-5, 100, 100000, 9007199254740993, 18446744073709551615,
			1234567890123456789012345678.0123456789,
			1.5, 2.5, 'ação 漢字 🐳', '2024-02-29',
			'2024-02-29 13:45:30.123456', '2024-02-29 13:45:30.123456',
			0x000102ff, NULL
		)`,
		`CREATE TABLE ` + confDB + `.baleia_conformance_tx (n BIGINT) ENGINE=InnoDB`,
		`CREATE TABLE ` + confDB + `.baleia_conformance_table (id BIGINT NOT NULL, name VARCHAR(20)) ENGINE=InnoDB`,
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}
