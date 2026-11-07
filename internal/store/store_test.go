package store

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/bun/migrate"
)

func TestParseDialect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dsn  string
		want Dialect
		ok   bool
	}{
		{dsn: "postgres://user:pass@host:5432/db", want: Postgres, ok: true},
		{dsn: "postgresql://user:pass@host:5432/db?sslmode=disable", want: Postgres, ok: true},
		{dsn: "POSTGRES://host/db", want: Postgres, ok: true},
		{dsn: "mysql://user:pass@host:3306/db", want: MySQL, ok: true},
		{dsn: "mariadb://user:pass@host:3306/db", want: MariaDB, ok: true},
		{dsn: "user:pass@tcp(host:3306)/db", want: MySQL, ok: true},
		{dsn: "user:pass@unix(/run/mysqld.sock)/db", want: MySQL, ok: true},
		{dsn: "sqlite://file::memory:", ok: false},
		{dsn: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.dsn, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDialect(tt.dsn)
			if !tt.ok {
				if err == nil {
					t.Fatalf("ParseDialect(%q) = %q, want error", tt.dsn, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDialect(%q): %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDialect(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestMySQLDSNFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want mysql.Config
	}{
		{
			in: "mysql://user:pass@host:3306/baleia?tls=false",
			want: mysql.Config{
				User: "user", Passwd: "pass", Net: "tcp", Addr: "host:3306",
				DBName: "baleia", ParseTime: true, TLSConfig: "false",
			},
		},
		{
			in: "mariadb://root:pw@db/baleia",
			want: mysql.Config{
				User: "root", Passwd: "pw", Net: "tcp", Addr: "db:3306",
				DBName: "baleia", ParseTime: true,
			},
		},
		{
			in: "mysql://user@host:13306/baleia?parseTime=false&foo=bar",
			want: mysql.Config{
				User: "user", Net: "tcp", Addr: "host:13306", DBName: "baleia",
				ParseTime: false, Params: map[string]string{"foo": "bar"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := mysqlDSN(tt.in)
			if err != nil {
				t.Fatalf("mysqlDSN(%q): %v", tt.in, err)
			}
			cfg, err := mysql.ParseDSN(got)
			if err != nil {
				t.Fatalf("ParseDSN(%q): %v", got, err)
			}
			if cfg.User != tt.want.User || cfg.Passwd != tt.want.Passwd ||
				cfg.Net != tt.want.Net || cfg.Addr != tt.want.Addr ||
				cfg.DBName != tt.want.DBName || cfg.ParseTime != tt.want.ParseTime ||
				cfg.TLSConfig != tt.want.TLSConfig {
				t.Fatalf("mysqlDSN(%q) = %+v, want %+v", tt.in, cfg, tt.want)
			}
			for k, v := range tt.want.Params {
				if cfg.Params[k] != v {
					t.Fatalf("mysqlDSN(%q) param %q = %q, want %q", tt.in, k, cfg.Params[k], v)
				}
			}
		})
	}
}

func TestMySQLDSNNativePassThrough(t *testing.T) {
	t.Parallel()
	const native = "user:pass@tcp(host:3306)/db?parseTime=true"
	got, err := mysqlDSN(native)
	if err != nil {
		t.Fatalf("mysqlDSN: %v", err)
	}
	if got != native {
		t.Fatalf("mysqlDSN(%q) = %q, want it unchanged", native, got)
	}
}

func TestApplyAndRollback(t *testing.T) {
	if os.Getenv("BALEIA_TEST_DATABASE_URL") == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool := testdb.New(t)
	db, err := Open(ctx, pool.Config().ConnString(), Options{MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	migrations := migrate.NewMigrations()
	if err := migrations.Discover(fstest.MapFS{
		"0001_store_test_widgets.up.sql": &fstest.MapFile{Data: []byte(
			`CREATE TABLE store_test_widgets (id BIGINT PRIMARY KEY, name VARCHAR(255) NOT NULL)`,
		)},
		"0001_store_test_widgets.down.sql": &fstest.MapFile{Data: []byte(
			`DROP TABLE store_test_widgets`,
		)},
	}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	applied, err := Apply(ctx, db, migrations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 || applied[0] != "0001" {
		t.Fatalf("Apply applied %v, want [0001]", applied)
	}

	count, err := db.NewSelect().TableExpr("store_test_widgets").Count(ctx)
	if err != nil {
		t.Fatalf("table was not created: %v", err)
	}
	if count != 0 {
		t.Fatalf("new table count = %d, want 0", count)
	}

	rolled, err := Rollback(ctx, db, migrations)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(rolled) != 1 || rolled[0] != "0001" {
		t.Fatalf("Rollback rolled back %v, want [0001]", rolled)
	}
}
