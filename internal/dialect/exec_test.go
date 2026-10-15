package dialect_test

// TestExecute_<dialect> runs the SQL each dialect generates on a real server:
// a golden string that the engine rejects is a bug the golden test cannot
// see. Servers come from the same BALEIA_TEST_*_URL variables as the
// provider integration tests (deploy/dev/sources, the Sources workflow);
// SQLite runs in memory, always.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	goora "github.com/sijms/go-ora/v2"
	_ "modernc.org/sqlite"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/trino"
)

// runner runs one statement with string parameters and returns its rows.
type runner func(ctx context.Context, sql string, args ...string) ([][]any, error)

func sqlRunner(db *sql.DB) runner {
	return func(ctx context.Context, q string, args ...string) ([][]any, error) {
		params := make([]any, len(args))
		for i, a := range args {
			params[i] = a
		}
		rows, err := db.QueryContext(ctx, q, params...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out [][]any
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			out = append(out, vals)
		}
		return out, rows.Err()
	}
}

func openSQL(t *testing.T, driver, dsn string) runner {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlRunner(db)
}

func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skip(name + " not set")
	}
	return v
}

// clickhouseRunner talks to the HTTP interface; parameters are {pN:String}.
func clickhouseRunner(t *testing.T, raw string) runner {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	return func(ctx context.Context, q string, args ...string) ([][]any, error) {
		v := url.Values{"default_format": {"JSONCompact"}}
		for i, a := range args {
			v.Set("param_p"+strconv.Itoa(i+1), a)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}).String()+"?"+v.Encode(), strings.NewReader(q))
		req.SetBasicAuth(u.User.Username(), pass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("clickhouse: %s", strings.TrimSpace(string(body)))
		}
		var out struct{ Data [][]any }
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		return out.Data, nil
	}
}

// trinoRunner prepares statements with parameters (EXECUTE ... USING).
func trinoRunner(raw string) runner {
	c := trino.New(raw, "baleia")
	return func(ctx context.Context, q string, args ...string) ([][]any, error) {
		opts := trino.ExecuteOptions{Catalog: "system"} // MetadataQueries read the session catalog
		if len(args) > 0 {
			opts.PreparedStatements = map[string]string{"q": q}
			lits := make([]string, len(args))
			for i, a := range args {
				lits[i] = trino.QuoteString(a)
			}
			q = "EXECUTE q USING " + strings.Join(lits, ", ")
		}
		res, err := c.ExecuteWithOptions(ctx, q, opts)
		if err != nil {
			return nil, err
		}
		return res.Data, nil
	}
}

// text renders a scanned value for comparison.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04:05")
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}

// fixture is everything an execution test needs per dialect.
type fixture struct {
	run      runner
	from     string              // FROM clause scalar selects need ("FROM dual" on Oracle)
	schema   string              // a schema MetadataQueries can list tables and columns of
	table    string              // a table in schema
	unsupTZ  bool                // the dialect has no time zone type
	dateText func(string) string // how the engine prints a truncated value, for comparison
}

func runDialect(t *testing.T, name string, fx fixture) {
	t.Helper()
	d := dialect.MustGet(name)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	one := func(t *testing.T, expr string) string {
		t.Helper()
		rows, err := fx.run(ctx, "SELECT "+expr+" "+fx.from)
		if err != nil {
			t.Fatalf("SELECT %s: %v", expr, err)
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			t.Fatalf("SELECT %s: rows %v", expr, rows)
		}
		return text(rows[0][0])
	}
	if fx.dateText == nil {
		fx.dateText = func(s string) string { return s }
	}

	t.Run("literals", func(t *testing.T) {
		ts := time.Date(2024, 3, 6, 10, 20, 30, 123456000, time.UTC)
		values := map[dialect.LogicalType]any{
			dialect.TypeBoolean: true, dialect.TypeInt64: int64(42), dialect.Decimal(18, 2): "12.50",
			dialect.TypeFloat64: 1.5, dialect.TypeString: `it's a \ test`, dialect.TypeDate: "2024-03-06",
			dialect.TypeTimestamp: ts, dialect.TypeTimestampTZ: ts, dialect.TypeBinary: []byte{0xca, 0xfe},
			dialect.TypeJSON: `{"a":1}`,
		}
		for typ, v := range values {
			lit, err := d.Literal(v, typ)
			if typ == dialect.TypeTimestampTZ && fx.unsupTZ {
				continue
			}
			if err != nil {
				t.Errorf("%s: %v", typ, err)
				continue
			}
			got := one(t, lit)
			if typ == dialect.TypeString && got != `it's a \ test` {
				t.Errorf("string literal round trip: %q", got)
			}
		}
	})

	t.Run("quoting", func(t *testing.T) {
		alias, err := d.QuoteIdent("a\"b]`c")
		if errors.Is(err, dialect.ErrInvalid) {
			alias, err = d.QuoteIdent("a b]`c") // the engine cannot quote " at all
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.run(ctx, "SELECT 1 AS "+alias+" "+fx.from); err != nil {
			t.Error(err)
		}
	})

	t.Run("time_trunc", func(t *testing.T) {
		lit, _ := d.Literal(time.Date(2024, 5, 15, 10, 20, 30, 0, time.UTC), dialect.TypeTimestamp) // a Wednesday
		want := map[dialect.TimeGrain]string{
			dialect.GrainSecond: "2024-05-15 10:20:30", dialect.GrainMinute: "2024-05-15 10:20", dialect.GrainHour: "2024-05-15 10:00",
			dialect.GrainDay: "2024-05-15", dialect.GrainWeek: "2024-05-13", dialect.GrainMonth: "2024-05-01",
			dialect.GrainQuarter: "2024-04-01", dialect.GrainYear: "2024-01-01",
		}
		for _, g := range dialect.Grains {
			expr, err := d.TimeTrunc(lit, g)
			if err != nil {
				t.Fatal(err)
			}
			if got := fx.dateText(one(t, expr)); !strings.HasPrefix(got, want[g]) {
				t.Errorf("%s: %s = %q, want %s…", g, expr, got, want[g])
			}
		}
	})

	t.Run("cast_divide_limit", func(t *testing.T) {
		for _, typ := range []dialect.LogicalType{dialect.TypeInt64, dialect.Decimal(18, 2), dialect.TypeFloat64, dialect.TypeString} {
			expr, err := d.Cast("1", typ)
			if err != nil {
				t.Fatal(err)
			}
			one(t, expr)
		}
		if got := one(t, d.SafeDivide("1", "0")); got != "NULL" && got != "<nil>" {
			t.Errorf("1/0 = %s, want NULL", got)
		}
		if got := one(t, d.SafeDivide("1", "2")); !strings.HasPrefix(got, "0.5") && got != ".5" {
			t.Errorf("1/2 = %s", got)
		}
		if _, err := fx.run(ctx, d.Limit("SELECT 1 AS x "+fx.from+" -- trailing comment", 1)); err != nil {
			t.Errorf("Limit: %v", err)
		}
		if q, ok := d.ExplainSQL("SELECT 1 " + fx.from); ok {
			if _, err := fx.run(ctx, q); err != nil {
				t.Errorf("Explain: %v", err)
			}
		}
	})

	t.Run("aggregates", func(t *testing.T) {
		rows := "SELECT 1 AS x " + fx.from + " UNION ALL SELECT 2 " + fx.from + " UNION ALL SELECT 2 " + fx.from
		for fn, want := range map[dialect.AggFunc]string{
			dialect.AggCountDistinct: "2", dialect.AggApproxCountDistinct: "2", dialect.AggSum: "5", dialect.AggMax: "2",
		} {
			agg, err := d.Aggregate(fn, "x", false)
			if err != nil {
				t.Fatal(err)
			}
			got, err := fx.run(ctx, "SELECT "+agg+" FROM ("+rows+") t")
			if err != nil {
				t.Errorf("%s: %v", fn, err)
				continue
			}
			if v := text(got[0][0]); v != want {
				t.Errorf("%s = %s, want %s", fn, v, want)
			}
		}
	})

	t.Run("metadata_queries", func(t *testing.T) {
		m := d.MetadataQueries()
		if _, err := fx.run(ctx, m.Schemas); err != nil {
			t.Errorf("schemas: %v", err)
		}
		tables, err := fx.run(ctx, m.Tables, fx.schema)
		if err != nil {
			t.Fatalf("tables: %v", err)
		}
		found := false
		for _, r := range tables {
			found = found || strings.EqualFold(text(r[0]), fx.table)
		}
		if !found {
			t.Errorf("tables of %s miss %s: %v", fx.schema, fx.table, tables)
		}
		cols, err := fx.run(ctx, m.Columns, fx.schema, fx.table)
		if err != nil || len(cols) == 0 {
			t.Errorf("columns: %v, %v", cols, err)
		}
		if m.Catalog != "" {
			all, err := fx.run(ctx, m.Catalog)
			if err != nil {
				t.Fatalf("catalog: %v", err)
			}
			n := 0
			for _, r := range all {
				if len(r) != 7 {
					t.Fatalf("catalog query row %v: want 7 fields", r)
				}
				if strings.EqualFold(text(r[0]), "information_schema") {
					t.Errorf("catalog query lists information_schema: %v", r)
				}
				if strings.EqualFold(text(r[0]), fx.schema) && strings.EqualFold(text(r[1]), fx.table) {
					n++
				}
			}
			// The catalog query leaves information_schema out on purpose.
			if !strings.EqualFold(fx.schema, "information_schema") && n != len(cols) {
				t.Errorf("catalog query lists %d columns of %s.%s, the columns query %d", n, fx.schema, fx.table, len(cols))
			}
		}
	})
}

func TestExecute_sqlite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // one in-memory database
	if _, err := db.Exec(`CREATE TABLE orders (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	runDialect(t, dialect.SQLite, fixture{run: sqlRunner(db), schema: "main", table: "orders", unsupTZ: true})
}

func TestExecute_postgresql(t *testing.T) {
	run := openSQL(t, "pgx", env(t, "BALEIA_TEST_POSTGRES_URL"))
	runDialect(t, dialect.PostgreSQL, fixture{run: run, schema: "information_schema", table: "tables"})
}

func mysqlDSN(t *testing.T, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	return fmt.Sprintf("%s:%s@tcp(%s)/mysql?parseTime=true", u.User.Username(), pass, u.Host)
}

func TestExecute_mysql(t *testing.T) {
	run := openSQL(t, "mysql", mysqlDSN(t, env(t, "BALEIA_TEST_MYSQL_URL")))
	runDialect(t, dialect.MySQL, fixture{run: run, schema: "mysql", table: "user", unsupTZ: true})
}

func TestExecute_mariadb(t *testing.T) {
	run := openSQL(t, "mysql", mysqlDSN(t, env(t, "BALEIA_TEST_MARIADB_URL")))
	runDialect(t, dialect.MariaDB, fixture{run: run, schema: "mysql", table: "user", unsupTZ: true})
}

func TestExecute_tsql(t *testing.T) {
	run := openSQL(t, "sqlserver", env(t, "BALEIA_TEST_SQLSERVER_URL")+"?encrypt=disable&database=master")
	runDialect(t, dialect.TSQL, fixture{run: run, schema: "dbo", table: "spt_values"})
}

func openOracle(t *testing.T, raw string) runner {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pass, _ := u.User.Password()
	return openSQL(t, "oracle", goora.BuildUrl(u.Hostname(), port, strings.TrimPrefix(u.Path, "/"), u.User.Username(), pass, nil))
}

func TestExecute_oracle(t *testing.T) {
	run := openOracle(t, env(t, "BALEIA_TEST_ORACLE_URL"))
	// SYS.DUAL exists on every server: the test creates nothing, so it cannot
	// disturb the provider tests that share the user's schema.
	runDialect(t, dialect.Oracle, fixture{run: run, from: "FROM dual", schema: "SYS", table: "DUAL"})
}

func TestExecute_clickhouse(t *testing.T) {
	run := clickhouseRunner(t, env(t, "BALEIA_TEST_CLICKHOUSE_URL"))
	runDialect(t, dialect.ClickHouse, fixture{run: run, schema: "system", table: "tables"})
}

func TestExecute_trino(t *testing.T) {
	run := trinoRunner(env(t, "BALEIA_TEST_TRINO_URL"))
	runDialect(t, dialect.Trino, fixture{run: run, schema: "information_schema", table: "tables"})
}
