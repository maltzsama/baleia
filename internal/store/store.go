// Package store opens Baleia's own metadata database through GORM (ADR 0020).
//
// GORM is the store ORM: models are the schema and persistence surface, and
// schema transitions are versioned Go migrations. The query-execution path
// (Trino and the direct engines) does not use this package.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Backend is a metadata-store backend Baleia supports and tests.
type Backend string

const (
	// Postgres is the default backend and the one used by local development.
	Postgres Backend = "postgres"
	// MySQL covers MySQL and MariaDB through GORM's MySQL dialector.
	MySQL Backend = "mysql"
)

// BackendFromDSN infers the backend from the connection string: a postgres://
// URL or the go-sql-driver DSN form (@tcp/@unix). The DSN is the single source
// of truth; there is no separate backend setting.
func BackendFromDSN(dsn string) (Backend, error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return Postgres, nil
	case strings.Contains(dsn, "@tcp("), strings.Contains(dsn, "@unix("):
		return MySQL, nil
	default:
		return "", fmt.Errorf("cannot infer the store backend from the connection string: use a postgres:// URL or the MySQL go-sql-driver DSN")
	}
}

// Options tune the GORM connection. Zero values keep GORM/database-sql
// defaults.
type Options struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	// Logger receives GORM's warnings and slow-query reports. Nil discards
	// them.
	Logger *slog.Logger
	// LogLevel overrides GORM's default (warn) when non-zero.
	LogLevel gormlogger.LogLevel
	// StatementTimeout, IdleInTxTimeout and LockTimeout are applied to every
	// PostgreSQL session of the store connection.
	StatementTimeout time.Duration
	IdleInTxTimeout  time.Duration
	LockTimeout      time.Duration
}

// Open connects to the store with the dialector selected by backend. Callers
// derive the backend from the DSN with BackendFromDSN; the DSN is the single
// source of truth.
func Open(ctx context.Context, dsn string, backend Backend, opts Options) (*gorm.DB, error) {
	if backend == Postgres {
		var err error
		dsn, err = postgresDSN(dsn, opts)
		if err != nil {
			return nil, err
		}
	}
	dialector, err := dialectorFor(dsn, backend)
	if err != nil {
		return nil, err
	}

	logLevel := opts.LogLevel
	if logLevel == 0 {
		logLevel = gormlogger.Warn
	}
	var gormLog gormlogger.Interface
	if opts.Logger != nil {
		gormLog = newSlogLogger(opts.Logger, logLevel)
	} else {
		gormLog = gormlogger.Discard
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:                                   gormLog,
		DisableAutomaticPing:                     true,
		TranslateError:                           true,
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s store: %w", backend, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("store connection pool: %w", err)
	}
	if opts.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}
	if opts.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(opts.ConnMaxIdleTime)
	}
	// GORM's automatic ping runs on a background context. Ping here instead so
	// a stalled backend cannot outlive the caller's deadline.
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping %s store: %w", backend, err)
	}
	return db, nil
}

// Close releases the underlying database-sql pool.
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// poolOnlyParams are accepted by connection-pool clients but are not PostgreSQL
// parameters: forwarding them makes the server reject the startup.
var poolOnlyParams = []string{
	"pool_max_conns",
	"pool_min_conns",
	"pool_max_conn_lifetime",
	"pool_max_conn_idle_time",
	"pool_health_check_period",
	"pool_max_conn_lifetime_jitter",
}

// postgresDSN strips pool-only parameters and applies the session timeouts
// through PostgreSQL's options parameter.
func postgresDSN(dsn string, opts Options) (string, error) {
	if !strings.Contains(dsn, "://") {
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse postgres store DSN: %w", err)
	}
	q := u.Query()
	for _, key := range poolOnlyParams {
		q.Del(key)
	}
	var settings []string
	for _, s := range []struct {
		name string
		d    time.Duration
	}{
		{"statement_timeout", opts.StatementTimeout},
		{"idle_in_transaction_session_timeout", opts.IdleInTxTimeout},
		{"lock_timeout", opts.LockTimeout},
	} {
		if s.d > 0 {
			settings = append(settings, "-c "+s.name+"="+strconv.FormatInt(s.d.Milliseconds(), 10))
		}
	}
	if len(settings) > 0 {
		if existing := strings.TrimSpace(q.Get("options")); existing != "" {
			settings = append([]string{existing}, settings...)
		}
		q.Set("options", strings.Join(settings, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func dialectorFor(dsn string, backend Backend) (gorm.Dialector, error) {
	switch backend {
	case Postgres:
		return postgres.Open(dsn), nil
	case MySQL:
		lower := strings.ToLower(dsn)
		if strings.HasPrefix(lower, "mysql://") || strings.HasPrefix(lower, "mariadb://") {
			return nil, fmt.Errorf("the mysql store DSN must use the go-sql-driver format (user:pass@tcp(host:port)/db), not a URL")
		}
		normalized := mysqlDSN(dsn)
		// Skip the dialector's version query: it runs on a background context.
		return mysql.New(mysql.Config{DSN: normalized, SkipInitializeWithVersion: true}), nil
	default:
		return nil, fmt.Errorf("unsupported store backend %q", backend)
	}
}

// mysqlDSN forces parseTime so time.Time columns, including the migration
// ledger's applied_at, scan without the operator having to remember the flag.
// The value is appended last, so it wins over an explicit parseTime=false.
func mysqlDSN(dsn string) string {
	switch {
	case strings.HasSuffix(dsn, "?"), strings.HasSuffix(dsn, "&"):
		return dsn + "parseTime=true"
	case strings.Contains(dsn, "?"):
		return dsn + "&parseTime=true"
	default:
		return dsn + "?parseTime=true"
	}
}

const slowQueryThreshold = 500 * time.Millisecond

// slogLogger forwards GORM diagnostics to slog at their original severity.
type slogLogger struct {
	log   *slog.Logger
	level gormlogger.LogLevel
}

var _ gormlogger.Interface = (*slogLogger)(nil)

func newSlogLogger(log *slog.Logger, level gormlogger.LogLevel) *slogLogger {
	return &slogLogger{log: log, level: level}
}

func (l *slogLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

func (l *slogLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.level >= gormlogger.Info {
		l.log.InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *slogLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.level >= gormlogger.Warn {
		l.log.WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *slogLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.level >= gormlogger.Error {
		l.log.ErrorContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *slogLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()
	attrs := []any{"elapsed", elapsed, "sql", sql, "rows", rows}
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= gormlogger.Error:
		l.log.ErrorContext(ctx, "store query failed", append(attrs, "err", err)...)
	case elapsed > slowQueryThreshold && l.level >= gormlogger.Warn:
		l.log.WarnContext(ctx, "store slow query", attrs...)
	case l.level >= gormlogger.Info:
		l.log.InfoContext(ctx, "store query", attrs...)
	}
}
