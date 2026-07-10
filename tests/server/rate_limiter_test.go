package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/server"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	ip := "192.168.1.1"

	for i := 0; i < server.RefreshRateBurst; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("request %d should be allowed (burst=%d)", i+1, server.RefreshRateBurst)
		}
	}

	if rl.Allow(ip) {
		t.Fatal("request should be rate-limited after burst exhausted")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping refill test in short mode")
	}

	rl := server.NewRateLimiter()
	defer rl.Stop()

	ip := "192.168.1.2"

	rl.Allow(ip)
	if rl.Allow(ip) {
		t.Fatal("second request should be rate-limited")
	}

	interval := server.RefreshRatePeriod / server.RefreshRateLimit
	time.Sleep(interval + 50*time.Millisecond)

	if !rl.Allow(ip) {
		t.Fatal("request should be allowed after refill interval")
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	ip1 := "10.0.0.1"
	ip2 := "10.0.0.2"

	rl.Allow(ip1)
	if rl.Allow(ip1) {
		t.Fatal("ip1 should be rate-limited")
	}

	if !rl.Allow(ip2) {
		t.Fatal("ip2 should not be affected by ip1 rate limit")
	}
}

func TestRateLimiterMiddlewareOnlyRefresh(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"refresh endpoint", "/api/v1/auth/refresh", http.StatusOK},
		{"login endpoint", "/api/v1/auth/login", http.StatusOK},
		{"other endpoint", "/api/v1/me", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.path, nil)
			req.RemoteAddr = "192.168.1.1:12345"
			rec := httptest.NewRecorder()
			middleware(handler).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d for %s", tt.wantStatus, rec.Code, tt.path)
			}
		})
	}
}

func TestRateLimiterMiddlewareBlocksRefresh(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	for i := 0; i < server.RefreshRateBurst; i++ {
		rec := httptest.NewRecorder()
		middleware(handler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	middleware(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	rl.Allow("stale-ip")
	time.Sleep(10 * time.Millisecond)

	if rl.IPCount() != 1 {
		t.Fatal("expected 1 entry")
	}

	rl.Cleanup(1 * time.Millisecond)

	if rl.IPCount() != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", rl.IPCount())
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				rl.Allow("concurrent-ip")
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if rl.IPCount() != 1 {
		t.Errorf("expected 1 IP entry, got %d", rl.IPCount())
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	ip := "10.0.0.99"

	rl.Allow(ip)
	if rl.Allow(ip) {
		t.Fatal("same IP should reuse rate-limited limiter")
	}
}

func TestRateLimiterMiddlewareNonRefreshUnlimited(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	req.RemoteAddr = "192.168.1.200:12345"

	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		middleware(handler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("non-refresh request %d should never be rate-limited, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimiterDefaultBurst(t *testing.T) {
	rl := server.NewRateLimiter()
	defer rl.Stop()

	ip := "172.16.0.1"

	count := 0
	for i := 0; i < 100; i++ {
		if rl.Allow(ip) {
			count++
		}
	}

	if count < 1 {
		t.Fatal("burst should allow at least 1 request")
	}
}
