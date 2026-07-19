package api

import (
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

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
