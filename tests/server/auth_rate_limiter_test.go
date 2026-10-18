package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/server"
)

func TestLogin_RateLimitedPerIP(t *testing.T) {
	var limited int
	var limitedKind string
	rl := server.NewAuthRouteLimiter([]server.AuthRouteLimit{
		{Path: "/api/v1/auth/login", Kind: "password", Every: time.Minute, Burst: 10},
	}, func(kind string) { limited++; limitedKind = kind })
	defer rl.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := rl.Middleware(handler)

	send := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 10; i++ {
		if rec := send("192.168.1.10"); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, rec.Code)
		}
	}

	rec := send("192.168.1.10")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th request = %d, want 429", rec.Code)
	}
	// One token refills every minute / 10 = 6 seconds.
	if got := rec.Header().Get("Retry-After"); got != "6" {
		t.Errorf("Retry-After = %q, want 6", got)
	}
	if limited != 1 || limitedKind != "password" {
		t.Errorf("limited hook = %d (%q), want 1 (password)", limited, limitedKind)
	}

	// Another IP has its own bucket.
	if rec := send("192.168.1.11"); rec.Code != http.StatusOK {
		t.Errorf("a different IP = %d, want 200", rec.Code)
	}
}

func TestReset_RateLimitedPerIP(t *testing.T) {
	rl := server.NewAuthRouteLimiter([]server.AuthRouteLimit{
		{Path: "/api/v1/auth/password-reset/request", Kind: "password_reset_request", Every: 15 * time.Minute, Burst: 5},
		{Path: "/api/v1/auth/password-reset/confirm", Kind: "password_reset_confirm", Every: 15 * time.Minute, Burst: 10},
	}, nil)
	defer rl.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := rl.Middleware(handler)

	send := func(path, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 5; i++ {
		if rec := send("/api/v1/auth/password-reset/request", "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("request error %d = %d, want 200", i+1, rec.Code)
		}
	}
	rec := send("/api/v1/auth/password-reset/request", "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "180" {
		t.Errorf("request Retry-After = %q, want 180", got)
	}

	for i := 0; i < 10; i++ {
		if rec := send("/api/v1/auth/password-reset/confirm", "10.0.0.2"); rec.Code != http.StatusOK {
			t.Fatalf("confirm error %d = %d, want 200", i+1, rec.Code)
		}
	}
	rec = send("/api/v1/auth/password-reset/confirm", "10.0.0.2")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th confirm = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Errorf("confirm Retry-After = %q, want 90", got)
	}
}

func TestAuthRouteLimiterIgnoresOtherRoutes(t *testing.T) {
	rl := server.NewAuthRouteLimiter([]server.AuthRouteLimit{
		{Path: "/api/v1/auth/login", Kind: "password", Every: time.Minute, Burst: 1},
	}, nil)
	defer rl.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := rl.Middleware(handler)

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("GET", "/api/v1/me", nil)
		req.RemoteAddr = "10.1.1.1:12345"
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unlimited route request %d = %d, want 200", i+1, rec.Code)
		}
	}
}
