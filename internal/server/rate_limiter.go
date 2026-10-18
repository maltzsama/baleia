package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimiterConfig struct {
	RequestsPerSecond int
	Burst             int
}

func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		RequestsPerSecond: 60,
		Burst:             10,
	}
}

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu    sync.Mutex
	perIP map[string]*ipLimiter
	cfg   RateLimiterConfig
	done  chan struct{}
}

func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{
		perIP: make(map[string]*ipLimiter),
		cfg:   cfg,
		done:  make(chan struct{}),
	}
	go rl.cleanup(5 * time.Minute)
	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.perIP[ip]
	if !ok {
		entry = &ipLimiter{
			limiter: rate.NewLimiter(rate.Limit(rl.cfg.RequestsPerSecond), rl.cfg.Burst),
		}
		rl.perIP[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

func (rl *RateLimiter) IPCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.perIP)
}

func (rl *RateLimiter) Cleanup(age time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-age)
	for ip, entry := range rl.perIP {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.perIP, ip)
		}
	}
}

func (rl *RateLimiter) Stop() {
	close(rl.done)
}

func (rl *RateLimiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.Cleanup(interval)
		case <-rl.done:
			return
		}
	}
}

// AuthRouteLimit is one strict, per-IP token bucket on an auth route (#51):
// Burst requests per Every. These limits are per process; a multi-replica
// deployment enforces them per replica until a shared limiter exists.
type AuthRouteLimit struct {
	// Path is the exact request path the bucket applies to.
	Path string
	// Kind is the bounded auth kind reported to metrics when a request is
	// refused.
	Kind string
	// Every is the window Burst tokens are refilled over.
	Every time.Duration
	// Burst is the bucket capacity.
	Burst int
}

type routeBucket struct {
	limit AuthRouteLimit
	mu    sync.Mutex
	perIP map[string]*ipLimiter
}

// AuthRouteLimiter enforces one token bucket per route and client IP. The
// paths are exact matches, so a route not listed here is unaffected.
type AuthRouteLimiter struct {
	buckets   []*routeBucket
	onLimited func(kind string)
	done      chan struct{}
}

// NewAuthRouteLimiter builds the strict auth-route limiter. onLimited, when
// set, observes the kind of every refused request.
func NewAuthRouteLimiter(limits []AuthRouteLimit, onLimited func(kind string)) *AuthRouteLimiter {
	l := &AuthRouteLimiter{onLimited: onLimited, done: make(chan struct{})}
	for _, limit := range limits {
		if limit.Every <= 0 || limit.Burst <= 0 {
			continue
		}
		l.buckets = append(l.buckets, &routeBucket{limit: limit, perIP: make(map[string]*ipLimiter)})
	}
	go l.cleanup(5 * time.Minute)
	return l
}

// Middleware refuses a request with 429 and Retry-After once its route's
// bucket is exhausted.
func (l *AuthRouteLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket := l.bucketFor(r.URL.Path)
		if bucket == nil {
			next.ServeHTTP(w, r)
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
		if retryAfter, ok := bucket.allow(ip); !ok {
			if l.onLimited != nil {
				l.onLimited(bucket.limit.Kind)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"code":"rate_limited","message":"too many requests"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *AuthRouteLimiter) bucketFor(path string) *routeBucket {
	for _, b := range l.buckets {
		if b.limit.Path == path {
			return b
		}
	}
	return nil
}

// Stop halts the stale-entry cleanup goroutine.
func (l *AuthRouteLimiter) Stop() { close(l.done) }

func (l *AuthRouteLimiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			for _, b := range l.buckets {
				// A bucket fully refills in Every, so an entry untouched for
				// that long is equivalent to a fresh one. Never prune a
				// 15-minute bucket at the 5-minute tick or the limit resets
				// early.
				age := b.limit.Every
				if age < interval {
					age = interval
				}
				cutoff := now.Add(-age)
				b.mu.Lock()
				for ip, entry := range b.perIP {
					if entry.lastSeen.Before(cutoff) {
						delete(b.perIP, ip)
					}
				}
				b.mu.Unlock()
			}
		case <-l.done:
			return
		}
	}
}

// allow reports whether the IP may proceed, and when it may retry otherwise.
func (b *routeBucket) allow(ip string) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.perIP[ip]
	if !ok {
		entry = &ipLimiter{
			limiter: rate.NewLimiter(rate.Limit(float64(b.limit.Burst)/b.limit.Every.Seconds()), b.limit.Burst),
		}
		b.perIP[ip] = entry
	}
	entry.lastSeen = time.Now()
	if entry.limiter.Allow() {
		return 0, true
	}
	// One token is refilled every Every/Burst.
	return b.limit.Every / time.Duration(b.limit.Burst), false
}

// onRefreshLimited, when set, observes refresh attempts the limiter refused.
func RateLimitRefreshMiddleware(rl *RateLimiter, onRefreshLimited func()) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limited := r.URL.Path == "/api/v1/auth/refresh" ||
				strings.HasSuffix(r.URL.Path, "/results") ||
				strings.Contains(r.URL.Path, "/ws/workspaces/")

			if !limited {
				next.ServeHTTP(w, r)
				return
			}

			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip == "" {
				ip = r.RemoteAddr
			}

			if !rl.Allow(ip) {
				if onRefreshLimited != nil && r.URL.Path == "/api/v1/auth/refresh" {
					onRefreshLimited()
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"code":"rate_limited","message":"too many refresh requests"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
