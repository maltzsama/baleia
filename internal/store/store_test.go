package store

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
)

func TestBackendFromDSN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dsn     string
		want    Backend
		wantErr bool
	}{
		{name: "postgres url", dsn: "postgres://u:p@localhost:5432/db", want: Postgres},
		{name: "postgresql url", dsn: "postgresql://u:p@localhost/db", want: Postgres},
		{name: "mysql tcp dsn", dsn: "u:p@tcp(127.0.0.1:3306)/db?parseTime=true", want: MySQL},
		{name: "mysql unix dsn", dsn: "u:p@unix(/var/run/mysqld/mysqld.sock)/db", want: MySQL},
		{name: "unknown", dsn: "file:test.db", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := BackendFromDSN(tt.dsn)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BackendFromDSN(%q) = %q, want error", tt.dsn, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BackendFromDSN(%q): %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Fatalf("BackendFromDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestDialectorForSelectsDriver(t *testing.T) {
	t.Parallel()
	d, err := dialectorFor("postgres://u:p@localhost/db", Postgres)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(*postgres.Dialector); !ok {
		t.Fatalf("dialectorFor(postgres) = %T, want *postgres.Dialector", d)
	}

	d, err = dialectorFor("u:p@tcp(127.0.0.1:3306)/db", MySQL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(*mysql.Dialector); !ok {
		t.Fatalf("dialectorFor(mysql) = %T, want *mysql.Dialector", d)
	}

	if _, err := dialectorFor("mysql://u:p@localhost/db", MySQL); err == nil {
		t.Fatal("dialectorFor(mysql URL) = nil error, want the go-sql-driver format error")
	}
	if _, err := dialectorFor("user:p://secret@tcp(127.0.0.1:3306)/db", MySQL); err != nil {
		t.Fatalf("dialectorFor rejected :// inside a password: %v", err)
	}
	if _, err := dialectorFor("postgres://u:p@localhost/db", ""); err == nil {
		t.Fatal("dialectorFor(unknown backend) = nil error, want error")
	}
}

func TestPostgresDSN(t *testing.T) {
	t.Parallel()
	got, err := postgresDSN(
		"postgres://u:p@localhost/db?sslmode=disable&pool_max_conns=10&application_name=baleia",
		Options{StatementTimeout: 5 * time.Second, LockTimeout: 2 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Has("pool_max_conns") {
		t.Fatal("postgresDSN kept pool_max_conns")
	}
	if q.Get("sslmode") != "disable" || q.Get("application_name") != "baleia" {
		t.Fatalf("postgresDSN dropped valid parameters: %s", u.RawQuery)
	}
	options := q.Get("options")
	if !strings.Contains(options, "statement_timeout=5000") || !strings.Contains(options, "lock_timeout=2000") {
		t.Fatalf("options = %q", options)
	}

	// The driver must read the same options: pgx 5.12 took a "+" literally and
	// the server rejected "+statement_timeout" at startup.
	cfg, err := pgconn.ParseConfig(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-c statement_timeout=5000 -c lock_timeout=2000"; cfg.RuntimeParams["options"] != want {
		t.Fatalf("pgx options = %q, want %q", cfg.RuntimeParams["options"], want)
	}
}

func TestMySQLDSNForcesParseTime(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"u:p@tcp(127.0.0.1:3306)/db", "u:p@tcp(127.0.0.1:3306)/db?parseTime=true"},
		{"u:p@tcp(127.0.0.1:3306)/db?parseTime=false", "u:p@tcp(127.0.0.1:3306)/db?parseTime=false&parseTime=true"},
		{"u:p@tcp(127.0.0.1:3306)/db?tls=false", "u:p@tcp(127.0.0.1:3306)/db?tls=false&parseTime=true"},
		{"u:p@tcp(127.0.0.1:3306)/db?", "u:p@tcp(127.0.0.1:3306)/db?parseTime=true"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := mysqlDSN(tt.in); got != tt.want {
				t.Fatalf("mysqlDSN(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
