package realtime

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheGet returns the cached value and whether it was present. A disabled
// client is a cache miss.
func (c *Client) CacheGet(ctx context.Context, key string) ([]byte, bool, error) {
	if !c.Enabled() {
		return nil, false, nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	value, err := c.rdb.Get(opCtx, c.key("cache", key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// CacheSet stores value under key for ttl. A disabled client is a no-op.
func (c *Client) CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if !c.Enabled() {
		return nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.Set(opCtx, c.key("cache", key), value, ttl).Err()
}

// CacheDel removes key. A disabled client is a no-op.
func (c *Client) CacheDel(ctx context.Context, key string) error {
	if !c.Enabled() {
		return nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.Del(opCtx, c.key("cache", key)).Err()
}

// CacheGetDel returns and removes key atomically, for single-use values such
// as WebSocket tickets. A disabled client is a cache miss.
func (c *Client) CacheGetDel(ctx context.Context, key string) ([]byte, bool, error) {
	if !c.Enabled() {
		return nil, false, nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	value, err := c.rdb.GetDel(opCtx, c.key("cache", key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}
