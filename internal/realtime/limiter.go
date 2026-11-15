package realtime

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucket refills a token bucket atomically. Arguments: rate (tokens per
// second), burst (bucket size), now (Unix milliseconds). It returns
// {allowed, retry-milliseconds}.
var tokenBucket = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local key = KEYS[1]
local data = redis.call('HMGET', key, 'tokens', 'ts', 'rate', 'burst')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
-- A policy/configuration change starts a fresh bucket immediately. Without
-- this check, a bucket created with an old burst would keep its old state.
if tokens == nil or data[3] ~= ARGV[1] or data[4] ~= ARGV[2] then
  tokens = burst
  ts = now
else
  local delta = math.max(0, now - ts) / 1000
  tokens = math.min(burst, tokens + delta * rate)
end
local allowed = 0
if tokens >= 1 then tokens = tokens - 1; allowed = 1 end
redis.call('HSET', key, 'tokens', tokens, 'ts', math.max(ts, now), 'rate', ARGV[1], 'burst', ARGV[2])
redis.call('PEXPIRE', key, math.ceil(burst / rate * 1000) + 1000)
local retry = 0
if allowed == 0 then retry = math.ceil((1 - tokens) / rate * 1000) end
return {allowed, retry}
`)

// Allow spends one token from key's bucket. rate is tokens per second and
// burst the bucket size. It returns whether the call is allowed and, when
// refused, how long until the next token. A disabled client always allows:
// there is no shared limit to enforce, so the caller's local limiter applies.
func (c *Client) Allow(ctx context.Context, key string, rate float64, burst int) (bool, time.Duration, error) {
	return c.allowAt(ctx, key, rate, burst, time.Now())
}

// allowAt is Allow with an explicit clock. Tests use it to model replicas whose
// clocks disagree: a lagging replica must never rewind the stored refill
// timestamp and make the next fast replica refill tokens it did not earn.
func (c *Client) allowAt(ctx context.Context, key string, rate float64, burst int, now time.Time) (bool, time.Duration, error) {
	if !c.Enabled() || rate <= 0 || burst <= 0 {
		return true, 0, nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	res, err := tokenBucket.Run(opCtx, c.rdb, []string{c.key("limit", key)}, rate, burst, now.UnixMilli()).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) < 2 {
		return false, 0, nil
	}
	if asInt64(res[0]) == 1 {
		return true, 0, nil
	}
	return false, time.Duration(asInt64(res[1])) * time.Millisecond, nil
}

// asInt64 reads an integer a Lua script returned.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
