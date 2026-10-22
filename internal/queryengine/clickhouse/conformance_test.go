package clickhouse

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
)

// TestConformance runs the engine suite against a real ClickHouse server. Set
// BALEIA_TEST_CLICKHOUSE_URL.
func TestConformance(t *testing.T) {
	c := testClient(t)
	setup(t, c)

	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		return queryengine.Guard(&Engine{dialect: dialect.NewRegistry().MustGet(dialect.ClickHouse), id: "conformance", c: c}, false)
	}, conformance.Fixtures{
		TypesSQL: `SELECT
			toInt64(9007199254740993) AS c_int64,
			toUInt64(18446744073709551615) AS c_uint64,
			toDecimal128('1234567890123456789012345678.0123456789', 10) AS c_decimal,
			toFloat64(1.5) AS c_float,
			'ção 漢字 🐳' AS c_text,
			toDate32('2024-02-29') AS c_date,
			toDateTime64('2024-02-29 13:45:30.123456', 6, 'UTC') AS c_dt,
			toNullable('x') AS c_nullable,
			CAST(NULL AS Nullable(String)) AS c_null`,
		Types: []conformance.TypeCase{
			{Column: "c_int64", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "c_uint64", Type: arrow.PrimitiveTypes.Uint64, Value: "18446744073709551615"},
			{Column: "c_decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "c_float", Type: arrow.PrimitiveTypes.Float64, Value: "1.5"},
			{Column: "c_text", Type: arrow.BinaryTypes.String, Value: "ção 漢字 🐳"},
			{Column: "c_date", Type: arrow.FixedWidthTypes.Date32, Value: "2024-02-29"},
			{Column: "c_dt", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "c_nullable", Type: arrow.BinaryTypes.String, Value: "x"},
			{Column: "c_null", Type: arrow.BinaryTypes.String, Value: "(null)"},
		},
		ZeroRowsSQL:       "SELECT toInt64(1) AS n WHERE 0",
		SetStateSQL:       "SET max_threads = 3",
		ReadStateSQL:      "SELECT getSetting('max_threads')",
		StateValue:        "3",
		ReadSQL:           "SELECT 1",
		WriteSQL:          "CREATE TABLE baleia_conformance.baleia_conformance_ro (a Int64) ENGINE = Memory",
		SleepSQL:          "SELECT sum(sipHash64(number)) FROM numbers(20000000000)",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "SELECT number FROM numbers(5000)",
		CollectCap:        1000,
		IntrospectCatalog: Catalog,
		IntrospectSchema:  []string{"baleia_conformance"},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// TestArrowPassthrough asserts the exact Arrow types ClickHouse emits reach the
// sink with no Baleia mapping layer.
func TestArrowPassthrough(t *testing.T) {
	c := testClient(t)
	e := queryengine.Guard(&Engine{dialect: dialect.NewRegistry().MustGet(dialect.ClickHouse), id: "passthrough", c: c}, false)
	tbl, err := queryengine.Collect(context.Background(), e, queryengine.SessionSpec{},
		`SELECT toUInt64(18446744073709551615) AS u,
		 toDecimal128('1.25', 2) AS d,
		 toDateTime64('2024-02-29 13:45:30.123456', 6, 'UTC') AS ts,
		 toNullable('x') AS s`, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	cases := []struct {
		col  int
		want arrow.DataType
	}{
		{0, arrow.PrimitiveTypes.Uint64},
		{1, &arrow.Decimal128Type{Precision: 38, Scale: 2}},
		{2, &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}},
		{3, arrow.BinaryTypes.String},
	}
	for _, tc := range cases {
		if got := tbl.Schema().Field(tc.col).Type; !arrow.TypeEqual(got, tc.want) {
			t.Errorf("column %d: %s, want %s", tc.col, got, tc.want)
		}
	}
}

// TestCredentialsNotInURL asserts the request URL never carries credentials.
func TestCredentialsNotInURL(t *testing.T) {
	cfg := connections.Config{
		Kind:    connections.KindClickHouse,
		Values:  map[string]string{"host": "ch.example", "port": "8123", "username": "alice"},
		Secrets: map[string]string{"password": "s3cret"},
	}
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(cfg, d)
	if got := c.base; got != "http://ch.example:8123/" {
		t.Fatalf("base = %q", got)
	}
	if got := c.user; got != "alice" {
		t.Fatalf("user = %q", got)
	}
	if _, err := url.Parse(c.base); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, c *client) {
	t.Helper()
	for _, stmt := range []string{
		"CREATE DATABASE IF NOT EXISTS baleia_conformance",
		"DROP TABLE IF EXISTS baleia_conformance.baleia_conformance_table",
		"CREATE TABLE baleia_conformance.baleia_conformance_table (id Int64, name Nullable(String)) ENGINE = Memory",
		"DROP TABLE IF EXISTS baleia_conformance.baleia_conformance_ro",
	} {
		if err := run(t, c, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func run(t *testing.T, c *client, sql string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.queryStrings(ctx, request{sql: sql})
	return err
}

// testClient builds a client from BALEIA_TEST_CLICKHOUSE_URL, skipping when it
// is unset.
func testClient(t *testing.T) *client {
	t.Helper()
	raw := os.Getenv("BALEIA_TEST_CLICKHOUSE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_CLICKHOUSE_URL not set")
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
	cfg := connections.Config{
		Kind: connections.KindClickHouse,
		Values: map[string]string{
			"host": host, "port": port, "secure": "false",
			"username": u.User.Username(), "database": "default",
		},
		Secrets: map[string]string{"password": pass},
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return newClient(cfg, d)
}
