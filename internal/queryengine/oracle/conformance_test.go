package oracle

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	goora "github.com/sijms/go-ora/v2"
)

// TestConformance runs the engine suite against a real Oracle Database. Set
// BALEIA_TEST_ORACLE_URL (oracle://user:pass@host:port/SERVICE).
func TestConformance(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "1521"
	}
	pass, _ := u.User.Password()
	user := u.User.Username()
	service := strings.TrimPrefix(u.Path, "/")
	setup(t, host, port, service, user, pass)

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := connections.Config{
		Kind: connections.KindOracle,
		Values: map[string]string{
			"host": host, "port": port, "service_name": service, "username": user,
		},
		Secrets: map[string]string{"password": pass},
	}
	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		e, err := newEngine(Options{}, dialect.NewRegistry().MustGet(dialect.Oracle))(context.Background(), queryengine.EngineRow{ID: "conformance", Kind: Kind}, cfg, d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}, conformance.Fixtures{
		TypesSQL: `SELECT c_num_ps AS "c_num_ps", c_num AS "c_num", c_bf AS "c_bf", c_bd AS "c_bd",
			c_vc AS "c_vc", c_nvc AS "c_nvc", c_ch AS "c_ch", c_clob AS "c_clob", c_raw AS "c_raw",
			c_blob AS "c_blob", c_date AS "c_date", c_ts AS "c_ts", c_tstz AS "c_tstz",
			c_tsltz AS "c_tsltz", c_null AS "c_null" FROM baleia_conformance_types`,
		Types: []conformance.TypeCase{
			{Column: "c_num_ps", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "c_num", Type: arrow.BinaryTypes.String, Value: "12345678901234567890.123456789"},
			{Column: "c_bf", Type: arrow.PrimitiveTypes.Float32, Value: "1.5"},
			{Column: "c_bd", Type: arrow.PrimitiveTypes.Float64, Value: "2.5"},
			{Column: "c_vc", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "c_nvc", Type: arrow.BinaryTypes.String, Value: "ação"},
			{Column: "c_ch", Type: arrow.BinaryTypes.String, Value: "abc"},
			{Column: "c_clob", Type: arrow.BinaryTypes.String, Value: "long text"},
			{Column: "c_raw", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
			{Column: "c_blob", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
			{Column: "c_date", Type: &arrow.TimestampType{Unit: arrow.Second}, Value: "2024-02-29T13:45:30"},
			{Column: "c_ts", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: "2024-02-29T13:45:30.123456"},
			{Column: "c_tstz", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "c_tsltz", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "c_null", Type: arrow.BinaryTypes.String, Value: "(null)"},
		},
		ZeroRowsSQL:       "SELECT 1 AS n FROM dual WHERE 1 = 0",
		SetStateSQL:       "ALTER SESSION SET TIME_ZONE = '+03:00'",
		ReadStateSQL:      "SELECT SESSIONTIMEZONE FROM dual",
		StateValue:        "+03:00",
		SleepSQL:          "SELECT COUNT(*) FROM all_objects a, all_objects b, all_objects c",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "SELECT LEVEL AS n FROM dual CONNECT BY LEVEL <= 5000",
		CollectCap:        1000,
		IntrospectCatalog: service,
		IntrospectSchema:  []string{strings.ToUpper(user)},
		IntrospectTable:   "BALEIA_CONFORMANCE_TABLE",
		IntrospectColumns: []string{"ID", "NAME"},
	})
}

// setup creates the fixture tables in the user's schema.
func setup(t *testing.T, host, port, service, user, pass string) {
	t.Helper()
	ctx := context.Background()
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("oracle", goora.BuildUrl(host, p, service, user, pass, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() {
		for _, s := range []string{
			`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_conformance_types'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
			`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_conformance_table'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
		} {
			_, _ = db.Exec(s)
		}
	})
	for _, stmt := range []string{
		`ALTER SESSION SET TIME_ZONE = '+00:00'`,
		`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_conformance_types'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
		`CREATE TABLE baleia_conformance_types (
			c_num_ps NUMBER(38,10), c_num NUMBER, c_bf BINARY_FLOAT, c_bd BINARY_DOUBLE,
			c_vc VARCHAR2(50), c_nvc NVARCHAR2(50), c_ch CHAR(3), c_clob CLOB, c_raw RAW(4), c_blob BLOB,
			c_date DATE, c_ts TIMESTAMP(6), c_tstz TIMESTAMP(6) WITH TIME ZONE,
			c_tsltz TIMESTAMP(6) WITH LOCAL TIME ZONE, c_null VARCHAR2(10)
		)`,
		`INSERT INTO baleia_conformance_types VALUES (
			1234567890123456789012345678.0123456789, 12345678901234567890.123456789, 1.5, 2.5,
			'ação 漢字 🐳', 'ação', 'abc', 'long text', HEXTORAW('000102ff'), HEXTORAW('000102ff'),
			TO_DATE('2024-02-29 13:45:30', 'YYYY-MM-DD HH24:MI:SS'),
			TIMESTAMP '2024-02-29 13:45:30.123456',
			TIMESTAMP '2024-02-29 13:45:30.123456 +00:00',
			TIMESTAMP '2024-02-29 13:45:30.123456',
			NULL
		)`,
		`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_conformance_table'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
		`CREATE TABLE baleia_conformance_table (id NUMBER(19), name VARCHAR2(20))`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}
