package realtime

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// openLimiterClient opens a client against the shared Redis and skips when the
// suite runs without one. This is an internal test because the regression it
// covers needs an explicit clock.
func openLimiterClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	c, err := Open(context.Background(), Options{
		URL:    url,
		Prefix: fmt.Sprintf("baleia-limiter-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLimiterTimestampNeverMovesBackward covers two replicas whose clocks
// disagree: the lagging call must not rewind the stored refill timestamp, and
// the next call on the fast replica must not refill tokens it did not earn.
func TestLimiterTimestampNeverMovesBackward(t *testing.T) {
	t.Parallel()
	c := openLimiterClient(t)
	ctx := context.Background()
	const key = "monotonic"
	t0 := time.Unix(1_700_000_000, 0)
	// Burst 1 at one token per second: the first call empties the bucket.
	if allowed, _, err := c.allowAt(ctx, key, 1, 1, t0); err != nil || !allowed {
		t.Fatalf("first call allowed=%v err=%v, want allowed", allowed, err)
	}
	// A replica one second behind consumes nothing and must not rewind ts.
	if allowed, _, err := c.allowAt(ctx, key, 1, 1, t0.Add(-time.Second)); err != nil || allowed {
		t.Fatalf("lagging call allowed=%v err=%v, want refused", allowed, err)
	}
	stored, err := c.rdb.HGet(ctx, c.key("limit", key), "ts").Int64()
	if err != nil {
		t.Fatalf("read ts: %v", err)
	}
	if stored != t0.UnixMilli() {
		t.Fatalf("stored ts = %d, want %d (lagging call moved it back)", stored, t0.UnixMilli())
	}
	// Half a second later the bucket earned half a token. If the lagging write
	// had rewound ts, it would see 1.5 tokens and admit the call.
	if allowed, _, err := c.allowAt(ctx, key, 1, 1, t0.Add(500*time.Millisecond)); err != nil || allowed {
		t.Fatalf("fast call allowed=%v err=%v, want refused", allowed, err)
	}
}
