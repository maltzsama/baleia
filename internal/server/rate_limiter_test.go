package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

func openSharedLimiterClient(t *testing.T, prefix string) *realtime.Client {
	t.Helper()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	c, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: prefix})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRateLimiterLocalWithoutRedis(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(RateLimiterConfig{RequestsPerSecond: 1, Burst: 2}, nil)
	defer rl.Stop()
	ctx := context.Background()
	if !rl.Allow(ctx, "ip") {
		t.Fatal("the first call must be allowed")
	}
	if !rl.Allow(ctx, "ip") {
		t.Fatal("the second call must be allowed")
	}
	if rl.Allow(ctx, "ip") {
		t.Fatal("the third call must be refused (burst 2)")
	}
}

func TestRateLimiterAcrossReplicas(t *testing.T) {
	t.Parallel()
	prefix := "server-rl-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	a := openSharedLimiterClient(t, prefix)
	b := openSharedLimiterClient(t, prefix)
	ctx := context.Background()
	rlA := NewRateLimiter(RateLimiterConfig{RequestsPerSecond: 1, Burst: 2}, a)
	rlB := NewRateLimiter(RateLimiterConfig{RequestsPerSecond: 1, Burst: 2}, b)
	defer rlA.Stop()
	defer rlB.Stop()

	if !rlA.Allow(ctx, "ip") {
		t.Fatal("replica A: the first call must be allowed")
	}
	if !rlA.Allow(ctx, "ip") {
		t.Fatal("replica A: the second call must be allowed")
	}
	if rlB.Allow(ctx, "ip") {
		t.Fatal("replica B must see the budget replica A spent")
	}
	if !rlB.Allow(ctx, "other-ip") {
		t.Fatal("another IP has its own bucket")
	}
}

func TestAuthRouteLimiterAcrossReplicas(t *testing.T) {
	t.Parallel()
	prefix := "server-auth-rl-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	a := openSharedLimiterClient(t, prefix)
	b := openSharedLimiterClient(t, prefix)
	limits := []AuthRouteLimit{{Path: "/api/v1/auth/login", Kind: "password", Every: time.Minute, Burst: 2}}
	la := NewAuthRouteLimiter(limits, nil, a)
	lb := NewAuthRouteLimiter(limits, nil, b)
	defer la.Stop()
	defer lb.Stop()

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	send := func(mw http.Handler, ip string) int {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}
	mwA := la.Middleware(ok)
	mwB := lb.Middleware(ok)
	for i := 0; i < 2; i++ {
		if got := send(mwA, "10.9.9.9"); got != http.StatusOK {
			t.Fatalf("replica A request %d = %d, want 200", i+1, got)
		}
	}
	if got := send(mwB, "10.9.9.9"); got != http.StatusTooManyRequests {
		t.Fatalf("replica B request = %d, want 429", got)
	}
}
