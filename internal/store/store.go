// Package store owns Baleia's connection to its own metadata store (#754): one
// data layer over database/sql, backed by Bun, that runs on PostgreSQL, MySQL
// and MariaDB. Domains get a *bun.DB and never open their own connection.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/schema"
)

// Dialect is the SGBD the metadata store runs on (docs/adr/0020).
type Dialect string

const (
	// Postgres is PostgreSQL.
	Postgres Dialect = "postgres"
	// MySQL is MySQL.
	MySQL Dialect = "mysql"
	// MariaDB is MariaDB, which speaks the MySQL protocol.
	MariaDB Dialect = "mariadb"
)

// Options are the connection pool limits; zero values keep the database/sql
// defaults.
type Options struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// ParseDialect reads the dialect from a DSN. URLs carry their scheme
// (postgres://, postgresql://, mysql://, mariadb://); a native go-sql-driver
// DSN (`user:pass@tcp(host:port)/db`) is MySQL.
func ParseDialect(dsn string) (Dialect, error) {
	trimmed := strings.TrimSpace(dsn)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return Postgres, nil
	case strings.HasPrefix(lower, "mysql://"):
		return MySQL, nil
	case strings.HasPrefix(lower, "mariadb://"):
		return MariaDB, nil
	case strings.Contains(lower, "@tcp("), strings.Contains(lower, "@unix("):
		return MySQL, nil
	default:
		return "", fmt.Errorf("store: unsupported DSN: want postgres://, postgresql://, mysql:// or mariadb://")
	}
}

// Open connects to the store with the dialect of dsn and returns the Bun
// handle the domains and the migrator share.
func Open(ctx context.Context, dsn string, opts Options) (*bun.DB, error) {
	dialect, err := ParseDialect(dsn)
	if err != nil {
		return nil, err
	}

	var (
		sqldb *sql.DB
		bd    schema.Dialect
	)
	switch dialect {
	case Postgres:
		sqldb = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
		bd = pgdialect.New()
	case MySQL, MariaDB:
		dsn, err := mysqlDSN(dsn)
		if err != nil {
			return nil, err
		}
		sqldb, err = sql.Open("mysql", dsn)
		if err != nil {
			return nil, fmt.Errorf("store: mysql: %w", err)
		}
		bd = mysqldialect.New()
	default:
		return nil, fmt.Errorf("store: unsupported dialect %q", dialect)
	}

	opts.apply(sqldb)

	db := bun.NewDB(sqldb, bd)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", dialect, err)
	}
	return db, nil
}

// WithTx runs fn in a transaction and commits it, rolling back on error.
func WithTx(ctx context.Context, db *bun.DB, fn func(context.Context, bun.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (o Options) apply(db *sql.DB) {
	if o.MaxOpenConns > 0 {
		db.SetMaxOpenConns(o.MaxOpenConns)
	}
	if o.MaxIdleConns > 0 {
		db.SetMaxIdleConns(o.MaxIdleConns)
	}
	if o.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(o.ConnMaxLifetime)
	}
	if o.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	}
}

// mysqlDSN turns a mysql:// or mariadb:// URL into the native go-sql-driver
// DSN; a DSN that is already native passes through untouched.
func mysqlDSN(dsn string) (string, error) {
	trimmed := strings.TrimSpace(dsn)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "mysql://") && !strings.HasPrefix(lower, "mariadb://") {
		return trimmed, nil
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("store: parse mysql url: %w", err)
	}

	cfg := mysql.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			cfg.Passwd = pass
		}
	}
	cfg.Net = "tcp"
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	cfg.Addr = addr
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	// Migrations send a whole schema block in one Exec; the driver needs this
	// to accept multiple statements. Bun still parameterizes every app query.
	cfg.MultiStatements = true

	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		switch key {
		case "parseTime":
			cfg.ParseTime = value == "true"
		case "tls":
			cfg.TLSConfig = value
		default:
			if cfg.Params == nil {
				cfg.Params = make(map[string]string, len(u.Query()))
			}
			cfg.Params[key] = value
		}
	}
	return cfg.FormatDSN(), nil
}
