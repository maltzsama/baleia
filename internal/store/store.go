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

// ParseBackend validates a configured backend name. An empty name selects
// PostgreSQL, preserving the previous default.
func ParseBackend(name string) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", string(Postgres), "postgresql":
		return Postgres, nil
	case string(MySQL), "mariadb":
		return MySQL, nil
	default:
		return "", fmt.Errorf("unsupported store backend %q (want postgres or mysql)", name)
	}
}

// BackendFromDSN infers the backend from a connection string. PostgreSQL
// accepts URL form; MySQL accepts the go-sql-driver DSN form. The ADR prefers
// an explicit BALEIA_STORE_BACKEND, so this is only a fallback.
func BackendFromDSN(dsn string) (Backend, error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return Postgres, nil
	case strings.Contains(dsn, "@tcp("), strings.Contains(dsn, "@unix("):
		return MySQL, nil
	default:
		return "", fmt.Errorf("cannot infer the store backend from the connection string; set BALEIA_STORE_BACKEND to postgres or mysql")
	}
}

// ResolveBackend returns the configured backend, falling back to the DSN.
func ResolveBackend(configured, dsn string) (Backend, error) {
	if strings.TrimSpace(configured) != "" {
		return ParseBackend(configured)
	}
	return BackendFromDSN(dsn)
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
}

// Open connects to the store with the dialector selected by backend. The DSN
// is never used to pick the dialector by itself: ADR 0020 requires an explicit
// backend, and ResolveBackend is the supported inference fallback.
func Open(ctx context.Context, dsn string, backend Backend, opts Options) (*gorm.DB, error) {
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
