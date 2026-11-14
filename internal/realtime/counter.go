package realtime

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// counterIncr adds one and starts the window on the first increment, in one
// round trip, so a crash between the two never leaves a counter that never
// expires. Arguments: window in milliseconds.
var counterIncr = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n
`)

// CounterIncr adds one to the counter at key and returns the new value. The
// counter expires window after its first increment (a fixed window). A disabled
// client returns ErrDisabled: the caller counts locally.
func (c *Client) CounterIncr(ctx context.Context, key string, window time.Duration) (int64, error) {
	if !c.Enabled() {
		return 0, ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return counterIncr.Run(opCtx, c.rdb, []string{c.key("counter", key)}, window.Milliseconds()).Int64()
}

// CounterGet returns the counter at key, zero when it is absent or expired.
func (c *Client) CounterGet(ctx context.Context, key string) (int64, error) {
	if !c.Enabled() {
		return 0, ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	n, err := c.rdb.Get(opCtx, c.key("counter", key)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// CounterReset removes the counter at key.
func (c *Client) CounterReset(ctx context.Context, key string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.Del(opCtx, c.key("counter", key)).Err()
}
