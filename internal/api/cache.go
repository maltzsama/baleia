package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

// TTLStore is the in-process cache. It is the fallback when the shared Redis
// layer is disabled (ADR 0022).
type TTLStore[T any] struct {
	mu    sync.RWMutex
	items map[string]cacheEntry[T]
	ttl   time.Duration
}

func NewTTLStore[T any](ttl time.Duration) *TTLStore[T] {
	return &TTLStore[T]{
		items: make(map[string]cacheEntry[T]),
		ttl:   ttl,
	}
}

func (s *TTLStore[T]) Get(key string) (T, bool) {
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		var zero T
		return zero, false
	}
	return e.value, true
}

func (s *TTLStore[T]) Set(key string, value T) {
	s.mu.Lock()
	s.items[key] = cacheEntry[T]{value: value, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
}

func (s *TTLStore[T]) Invalidate(key string) {
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}

func (s *TTLStore[T]) Clear() {
	s.mu.Lock()
	clear(s.items)
	s.mu.Unlock()
}

// cacheStore is the Get/Set/Invalidate/Clear surface the handlers use: the
// local TTLStore or the shared Redis-backed cache (ADR 0022).
type cacheStore[T any] interface {
	Get(key string) (T, bool)
	Set(key string, value T)
	Invalidate(key string)
	Clear()
}

const (
	// cacheTimeout bounds one shared-cache operation: a slow or unreachable
	// Redis degrades to a miss instead of holding the request.
	cacheTimeout = 200 * time.Millisecond
	// cacheGenerationTTL is how long a replica reuses the generation it read;
	// a Clear on another replica is therefore seen within this window.
	cacheGenerationTTL = time.Second
)

// sharedCache is a cacheStore over the shared Redis layer. With a disabled
// client it is exactly a local TTLStore. With Redis it reads and writes only
// Redis (no local L1), so every replica sees the same value. Clear bumps a
// generation key in O(1) instead of deleting every entry; old entries expire
// on their TTL.
type sharedCache[T any] struct {
	client *realtime.Client
	local  *TTLStore[T]
	name   string
	ttl    time.Duration

	mu      sync.Mutex
	gen     string
	genTill time.Time
}

// newCache builds a cacheStore backed by the shared layer.
func newCache[T any](client *realtime.Client, name string, ttl time.Duration) cacheStore[T] {
	return &sharedCache[T]{client: client, local: NewTTLStore[T](ttl), name: name, ttl: ttl}
}

func (c *sharedCache[T]) Get(key string) (T, bool) {
	if !c.client.Enabled() {
		return c.local.Get(key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()
	raw, ok, err := c.client.CacheGet(ctx, c.cacheKey(ctx, key))
	if err != nil || !ok {
		var zero T
		return zero, false
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		var zero T
		return zero, false
	}
	return value, true
}

func (c *sharedCache[T]) Set(key string, value T) {
	if !c.client.Enabled() {
		c.local.Set(key, value)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()
	_ = c.client.CacheSet(ctx, c.cacheKey(ctx, key), raw, c.ttl)
}

func (c *sharedCache[T]) Invalidate(key string) {
	if !c.client.Enabled() {
		c.local.Invalidate(key)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()
	_ = c.client.CacheDel(ctx, c.cacheKey(ctx, key))
}

func (c *sharedCache[T]) Clear() {
	if !c.client.Enabled() {
		c.local.Clear()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()
	gen := rand.Text()
	if err := c.client.CacheSet(ctx, c.key("gen"), []byte(gen), 0); err != nil {
		// Keep the old generation: entries still expire on their TTL.
		return
	}
	c.mu.Lock()
	c.gen, c.genTill = gen, time.Now().Add(cacheGenerationTTL)
	c.mu.Unlock()
}

// cacheKey namespaces key under the current generation.
func (c *sharedCache[T]) cacheKey(ctx context.Context, key string) string {
	return c.name + ":" + c.generation(ctx) + ":" + key
}

// generation returns the cache generation, reusing the last read for a short
// time so a Get/Set is one Redis call, not two.
func (c *sharedCache[T]) generation(ctx context.Context) string {
	c.mu.Lock()
	if time.Now().Before(c.genTill) {
		gen := c.gen
		c.mu.Unlock()
		return gen
	}
	c.mu.Unlock()

	raw, ok, err := c.client.CacheGet(ctx, c.key("gen"))
	gen := "0"
	if err == nil && ok && len(raw) > 0 {
		gen = string(raw)
	}
	c.mu.Lock()
	c.gen, c.genTill = gen, time.Now().Add(cacheGenerationTTL)
	c.mu.Unlock()
	return gen
}

// key builds one of the cache's own keys (the generation key).
func (c *sharedCache[T]) key(parts ...string) string {
	return c.name + ":" + strings.Join(parts, ":")
}
