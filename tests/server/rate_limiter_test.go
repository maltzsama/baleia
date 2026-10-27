package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/galileostudio/baleia/internal/server"
)

func testConfig() server.RateLimiterConfig {
	return server.RateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}
}

func TestRateLimiterAllow(t *testing.T) {
	rl := server.NewRateLimiter(testConfig())
	defer rl.Stop()

	ip := "192.168.1.1"

	for i := 0; i < testConfig().Burst; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("request %d should be allowed (burst=%d)", i+1, testConfig().Burst)
		}
	}

	if rl.Allow(ip) {
		t.Fatal("request should be rate-limited after burst exhausted")
	}
}

// TestRateLimiterRefill runs on the bubble's clock: the refill second
// passes at once (#374).
func TestRateLimiterRefill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := server.NewRateLimiter(testConfig())
		defer rl.Stop()

		ip := "192.168.1.2"

		rl.Allow(ip)
		if rl.Allow(ip) {
			t.Fatal("second request should be rate-limited")
		}

		time.Sleep(time.Second - time.Millisecond)
		if rl.Allow(ip) {
			t.Fatal("request allowed before the refill interval")
		}
		time.Sleep(time.Millisecond)
		if !rl.Allow(ip) {
			t.Fatal("request should be allowed after refill interval")
		}
	})
}

func TestRateLimiterPerIP(t *testing.T) {
	rl := server.NewRateLimiter(testConfig())
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
	rl := server.NewRateLimiter(testConfig())
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		path       string
		remoteAddr string
		wantStatus int
	}{
		{"refresh endpoint", "/api/v1/auth/refresh", "192.168.1.1:12345", http.StatusOK},
		{"results endpoint", "/api/v1/workspaces/abc/executions/def/results", "192.168.1.2:12345", http.StatusOK},
		{"websocket endpoint", "/api/v1/ws/workspaces/abc/executions/def", "192.168.1.3:12345", http.StatusOK},
		{"login endpoint", "/api/v1/auth/login", "192.168.1.4:12345", http.StatusOK},
		{"other endpoint", "/api/v1/me", "192.168.1.5:12345", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.path, nil)
			req.RemoteAddr = tt.remoteAddr
			rec := httptest.NewRecorder()
			middleware(handler).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d for %s", tt.wantStatus, rec.Code, tt.path)
			}
		})
	}
}

func TestRateLimiterMiddlewareBlocksRefresh(t *testing.T) {
	cfg := testConfig()
	rl := server.NewRateLimiter(cfg)
	defer rl.Stop()

	limited := 0
	middleware := server.RateLimitRefreshMiddleware(rl, func() { limited++ })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	for i := 0; i < cfg.Burst; i++ {
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
	if limited != 1 {
		t.Fatalf("rate-limit hook ran %d times, want 1 (the refused refresh)", limited)
	}
}

func TestRateLimiterMiddlewareBlocksResults(t *testing.T) {
	cfg := testConfig()
	rl := server.NewRateLimiter(cfg)
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/api/v1/workspaces/uuid/executions/uuid/results", nil)
	req.RemoteAddr = "192.168.1.101:12345"

	for i := 0; i < cfg.Burst; i++ {
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
	synctest.Test(t, func(t *testing.T) {
		rl := server.NewRateLimiter(testConfig())
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
	})
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := server.NewRateLimiter(testConfig())
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
	rl := server.NewRateLimiter(testConfig())
	defer rl.Stop()

	ip := "10.0.0.99"

	rl.Allow(ip)
	if rl.Allow(ip) {
		t.Fatal("same IP should reuse rate-limited limiter")
	}
}

func TestRateLimiterMiddlewareNonRefreshUnlimited(t *testing.T) {
	rl := server.NewRateLimiter(testConfig())
	defer rl.Stop()

	middleware := server.RateLimitRefreshMiddleware(rl, nil)
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
	rl := server.NewRateLimiter(testConfig())
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

func TestRateLimiterConfigurable(t *testing.T) {
	cfg := server.RateLimiterConfig{
		RequestsPerSecond: 100,
		Burst:             50,
	}
	rl := server.NewRateLimiter(cfg)
	defer rl.Stop()

	ip := "10.0.0.200"

	for i := 0; i < cfg.Burst; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("request %d should be allowed with burst=%d", i+1, cfg.Burst)
		}
	}

	if rl.Allow(ip) {
		t.Fatal("should be rate-limited after burst")
	}
}
