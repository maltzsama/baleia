package server

import (
	"net"
	"net/http"
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

func RateLimitRefreshMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
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
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"code":"rate_limited","message":"too many refresh requests"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
