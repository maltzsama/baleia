// Package dbpool opens Baleia's own database pool with configurable limits
// (#87): BALEIA_DB_MAX_CONNS, BALEIA_DB_MIN_CONNS, BALEIA_DB_MAX_CONN_LIFETIME.
package dbpool

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options are the pool limits; zero values keep pgxpool's defaults.
type Options struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	// Tracer observes acquires (metrics.PoolTracer); nil for none.
	Tracer pgx.QueryTracer
}

// Open parses url, applies opts and connects.
func Open(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	if opts.MinConns > 0 {
		cfg.MinConns = opts.MinConns
	}
	if opts.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = opts.MaxConnLifetime
	}
	if opts.Tracer != nil {
		cfg.ConnConfig.Tracer = opts.Tracer
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}
