package postgresql

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	"github.com/jackc/pgx/v5"
)

// TestConformance runs the engine suite against a real PostgreSQL server.
// Set BALEIA_TEST_POSTGRES_URL.
func TestConformance(t *testing.T) {
	raw := os.Getenv("BALEIA_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_POSTGRES_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := dsn(raw, u)
	database := cfg.Value("database")
	setup(t, raw)

	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	conformance.Run(t, func(t *testing.T) queryengine.Engine {
		e, err := newEngine(dialect.NewRegistry().MustGet(dialect.PostgreSQL))(context.Background(), queryengine.EngineRow{ID: "conformance", Kind: Kind}, cfg, d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}, conformance.Fixtures{
		TypesSQL: `SELECT
			true AS "bool",
			9007199254740993::int8 AS "int64",
			1234567890123456789012345678.0123456789::numeric(38,10) AS "decimal",
			DATE '2024-02-29' AS "date",
			TIMESTAMP '2024-02-29 13:45:30.123456' AS "timestamp",
			TIMESTAMPTZ '2024-02-29 13:45:30.123456+00' AS "timestamptz",
			'ação 漢字 🐳' AS "varchar",
			NULL::text AS "null",
			'\x000102ff'::bytea AS "binary"`,
		Types: []conformance.TypeCase{
			{Column: "bool", Type: arrow.FixedWidthTypes.Boolean, Value: "true"},
			{Column: "int64", Type: arrow.PrimitiveTypes.Int64, Value: "9007199254740993"},
			{Column: "decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: "1234567890123456789012345678.0123456789"},
			{Column: "date", Type: arrow.FixedWidthTypes.Date32, Value: "2024-02-29"},
			{Column: "timestamp", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: "2024-02-29T13:45:30.123456"},
			{Column: "timestamptz", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: "2024-02-29T13:45:30.123456Z"},
			{Column: "varchar", Type: arrow.BinaryTypes.String, Value: "ação 漢字 🐳"},
			{Column: "null", Type: arrow.BinaryTypes.String, Value: "(null)"},
			{Column: "binary", Type: arrow.BinaryTypes.Binary, Value: "AAEC/w=="},
		},
		ZeroRowsSQL:       "SELECT 1 AS n WHERE false",
		SetStateSQL:       "SET application_name = 'baleia_conformance_state'",
		ReadStateSQL:      "SHOW application_name",
		StateValue:        "baleia_conformance_state",
		ReadSQL:           "SELECT 1",
		WriteSQL:          "CREATE TABLE public.baleia_conformance_ro (a integer)",
		SleepSQL:          "SELECT pg_sleep(60)",
		SyntaxErrorSQL:    "SELEKT 1",
		MissingTableSQL:   "SELECT * FROM baleia_missing_conformance",
		ManyRowsSQL:       "SELECT generate_series(1, 5000) AS n",
		CollectCap:        1000,
		BeginSQL:          "BEGIN",
		InsertSQL:         "INSERT INTO public.baleia_conformance_tx (n) VALUES (1)",
		CountSQL:          "SELECT count(*) FROM public.baleia_conformance_tx",
		IntrospectCatalog: database,
		IntrospectSchema:  []string{"public"},
		IntrospectTable:   "baleia_conformance_table",
		IntrospectColumns: []string{"id", "name"},
	})
}

// dsn builds the engine config from a connection URL. TLS is off: the test
// server has none.
func dsn(raw string, u *url.URL) connections.Config {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	user := "postgres"
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		db = "postgres"
	}
	return connections.Config{
		Kind:    connections.KindPostgreSQL,
		Values:  map[string]string{"host": host, "port": port, "database": db, "username": user, "sslmode": "disable"},
		Secrets: map[string]string{"password": pass},
	}
}

// setup creates the fixture tables the suite uses.
func setup(t *testing.T, raw string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, withSSLDisabled(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, sql := range []string{
		`CREATE TABLE IF NOT EXISTS public.baleia_conformance_tx (n bigint)`,
		`TRUNCATE public.baleia_conformance_tx`,
		`CREATE TABLE IF NOT EXISTS public.baleia_conformance_table (id bigint NOT NULL, name text)`,
		`DROP TABLE IF EXISTS public.baleia_conformance_ro`,
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
}

func withSSLDisabled(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}
