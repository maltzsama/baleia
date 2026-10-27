package oracle

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	goora "github.com/sijms/go-ora/v2"
)

func requireOracle(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("BALEIA_TEST_ORACLE_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_ORACLE_URL not set")
	}
	return raw
}

func oracleEngine(t *testing.T, raw string, o Options) queryengine.Engine {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()
	if port == "" {
		port = "1521"
	}
	pass, _ := u.User.Password()
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(o, dialect.NewRegistry().MustGet(dialect.Oracle))(context.Background(), queryengine.EngineRow{ID: "t", Kind: Kind}, connections.Config{
		Kind: connections.KindOracle,
		Values: map[string]string{
			"host": u.Hostname(), "port": port,
			"service_name": strings.TrimPrefix(u.Path, "/"), "username": u.User.Username(),
		},
		Secrets: map[string]string{"password": pass},
	}, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func oracleAdmin(t *testing.T, raw string) *sql.DB {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	dsn := goora.BuildUrl(u.Hostname(), atoiPort(t, u.Port()), strings.TrimPrefix(u.Path, "/"), u.User.Username(), pass, nil)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func atoiPort(t *testing.T, p string) int {
	t.Helper()
	if p == "" {
		return 1521
	}
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
	}
	return n
}

// TestNumberWithoutPrecisionExact: a NUMBER without precision is read as exact
// decimal text, never as float64.
func TestNumberWithoutPrecisionExact(t *testing.T) {
	e := oracleEngine(t, requireOracle(t), Options{})
	tbl, err := queryengine.Collect(context.Background(), e, queryengine.SessionSpec{},
		`SELECT 12345678901234567890.123456789 AS "n" FROM dual`, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	if got := tbl.Column(0).Data().Chunk(0).ValueStr(0); got != "12345678901234567890.123456789" {
		t.Errorf("n = %q", got)
	}
}

// TestLobCap: a CLOB larger than the byte cap is truncated and the truncation
// is flagged in the field metadata.
func TestLobCap(t *testing.T) {
	raw := requireOracle(t)
	db := oracleAdmin(t, raw)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = db.Exec(`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_lob_test'; EXCEPTION WHEN OTHERS THEN NULL; END;`)
	})
	for _, stmt := range []string{
		`BEGIN EXECUTE IMMEDIATE 'DROP TABLE baleia_lob_test'; EXCEPTION WHEN OTHERS THEN NULL; END;`,
		`CREATE TABLE baleia_lob_test (c CLOB)`,
		`INSERT INTO baleia_lob_test VALUES (RPAD('x', 4096, 'x'))`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	e := oracleEngine(t, raw, Options{LOBMaxBytes: 1024})
	tbl, err := queryengine.Collect(ctx, e, queryengine.SessionSpec{}, `SELECT c AS "c" FROM baleia_lob_test`, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer tbl.Release()
	if got := len(tbl.Column(0).Data().Chunk(0).ValueStr(0)); got != 1024 {
		t.Errorf("capped CLOB length = %d, want 1024", got)
	}
	if v, ok := tbl.Schema().Field(0).Metadata.GetValue("baleia.lob_truncated"); !ok || v != "true" {
		t.Errorf("lob_truncated flag = %q/%v, want true", v, ok)
	}
}
