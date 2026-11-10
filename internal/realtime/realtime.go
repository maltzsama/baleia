// Package realtime is Baleia's shared cross-replica backbone (ADR 0022). It
// wraps one Redis client and exposes the pub/sub, cache, rate-limit and
// Streams surfaces the real-time features use. A client built without a URL is
// disabled: callers treat it as "no shared layer" and fall back to the local
// behaviour.
package realtime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrDisabled is returned by an operation that needs the shared layer when no
// Redis address was configured.
var ErrDisabled = errors.New("realtime: shared layer is disabled")

const (
	defaultTimeout = 5 * time.Second
	defaultPrefix  = "baleia"
)

// Options configure the shared client.
type Options struct {
	// URL is BALEIA_REDIS_URL, e.g. redis://localhost:6379/0. Empty disables
	// the shared layer.
	URL string
	// Prefix namespaces every key and channel so deployments can share one
	// Redis instance. Empty uses "baleia".
	Prefix string
	// Timeout bounds one operation; zero uses 5s.
	Timeout time.Duration
}

// Client is the shared Redis client. It is never nil: a disabled client
// (empty URL) reports Enabled false and treats every operation as a no-op.
type Client struct {
	rdb     *redis.Client
	prefix  string
	timeout time.Duration
}

// Open builds the client. With an empty URL it returns a disabled client and
// no error. With a URL it connects and pings once, so a misconfigured address
// fails at startup instead of on the first request.
func Open(ctx context.Context, opts Options) (*Client, error) {
	c := &Client{
		prefix:  cmp.Or(opts.Prefix, defaultPrefix),
		timeout: cmp.Or(opts.Timeout, defaultTimeout),
	}
	if strings.TrimSpace(opts.URL) == "" {
		return c, nil
	}
	cfg, err := redis.ParseURL(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("realtime: parse BALEIA_REDIS_URL: %w", err)
	}
	c.rdb = redis.NewClient(cfg)
	if err := c.Ping(ctx); err != nil {
		_ = c.rdb.Close()
		return nil, fmt.Errorf("realtime: connect: %w", err)
	}
	return c, nil
}

// Enabled reports whether a shared layer is configured.
func (c *Client) Enabled() bool { return c != nil && c.rdb != nil }

// Ping proves the connection answers.
func (c *Client) Ping(ctx context.Context) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.Ping(opCtx).Err()
}

// Close releases the connection.
func (c *Client) Close() error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Close()
}

// operation bounds one call with the configured timeout.
func (c *Client) operation(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

// key namespaces a key under the deployment prefix.
func (c *Client) key(kind string, parts ...string) string {
	return c.prefix + ":" + kind + ":" + strings.Join(parts, ":")
}
