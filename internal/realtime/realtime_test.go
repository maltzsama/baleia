package realtime_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

// openClient returns a client against BALEIA_TEST_REDIS_URL, or skips when it
// is unset (the unit suite runs without Redis; CI provides one).
func openClient(t *testing.T) *realtime.Client {
	t.Helper()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	prefix := "baleia-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	c, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: prefix})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestDisabledClient(t *testing.T) {
	t.Parallel()
	c, err := realtime.Open(context.Background(), realtime.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if c.Enabled() {
		t.Fatal("a client without a URL must be disabled")
	}
	if err := c.Publish(context.Background(), "x", []byte("y")); err != nil {
		t.Fatalf("publish on a disabled client: %v", err)
	}
	if err := c.Subscribe(context.Background(), "x", func([]byte) {}); !errors.Is(err, realtime.ErrDisabled) {
		t.Fatalf("subscribe on a disabled client = %v, want ErrDisabled", err)
	}
	if _, ok, err := c.CacheGet(context.Background(), "x"); ok || err != nil {
		t.Fatalf("cache get on a disabled client = ok %v, err %v", ok, err)
	}
	if allowed, _, err := c.Allow(context.Background(), "x", 1, 1); !allowed || err != nil {
		t.Fatalf("allow on a disabled client = %v, %v", allowed, err)
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	c := openClient(t)
	ctx := context.Background()
	if err := c.CacheSet(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, ok, err := c.CacheGet(ctx, "k")
	if err != nil || !ok || string(value) != "v" {
		t.Fatalf("get = %q, %v, %v", value, ok, err)
	}
	if err := c.CacheDel(ctx, "k"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, ok, _ := c.CacheGet(ctx, "k"); ok {
		t.Fatal("deleted key is still present")
	}
}

func TestLimiter(t *testing.T) {
	t.Parallel()
	c := openClient(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		allowed, _, err := c.Allow(ctx, "bucket", 1, 2)
		if err != nil || !allowed {
			t.Fatalf("call %d allowed=%v err=%v, want allowed", i, allowed, err)
		}
	}
	allowed, retry, err := c.Allow(ctx, "bucket", 1, 2)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if allowed {
		t.Fatal("third call must be refused (burst 2)")
	}
	if retry <= 0 {
		t.Fatalf("retry after = %v, want > 0", retry)
	}
	time.Sleep(1100 * time.Millisecond)
	if allowed, _, err := c.Allow(ctx, "bucket", 1, 2); err != nil || !allowed {
		t.Fatalf("after refill allowed=%v err=%v, want allowed", allowed, err)
	}
}

func TestBus(t *testing.T) {
	t.Parallel()
	c := openClient(t)
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(subCtx, "channel", func(payload []byte) {
			select {
			case received <- string(payload):
			default:
			}
		})
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.Publish(context.Background(), "channel", []byte("hello")); err != nil {
			t.Fatalf("publish: %v", err)
		}
		select {
		case got := <-received:
			if got != "hello" {
				t.Fatalf("received %q", got)
			}
			cancel()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("subscribe returned %v", err)
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("no message received")
}

func TestStreams(t *testing.T) {
	t.Parallel()
	c := openClient(t)
	ctx := context.Background()
	if err := c.StreamEnsureGroup(ctx, "events", "relay"); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	id, err := c.StreamAdd(ctx, "events", map[string]any{"event": "execution.finished"}, 0)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	msgs, err := c.StreamReadGroup(ctx, "events", "relay", "consumer-a", 10, 0)
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Values["event"] != "execution.finished" {
		t.Fatalf("read = %#v", msgs)
	}
	// consumer-a never acknowledged it; consumer-b must be able to recover it.
	claimed, err := c.StreamClaimPending(ctx, "events", "relay", "consumer-b", 0, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("claimed = %#v", claimed)
	}
	if err := c.StreamAck(ctx, "events", "relay", id); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := c.StreamTrim(ctx, "events", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("trim: %v", err)
	}
}
