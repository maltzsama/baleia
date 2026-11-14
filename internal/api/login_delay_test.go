package api

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

// TestLogin_ProgressiveDelay: the sixth failure waits at least 1s, then the
// delay doubles and caps at 60s (#51).
func TestLogin_ProgressiveDelay(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	var slept []time.Duration
	d := NewLoginDelay(LoginDelayConfig{
		Now:   func() time.Time { return now },
		Sleep: func(_ context.Context, dur time.Duration) error { slept = append(slept, dur); return nil },
	})

	for i := 0; i < 5; i++ {
		d.Failure(context.Background(), "User@Example.test")
	}
	if err := d.Wait(context.Background(), "user@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] < time.Second {
		t.Fatalf("6th attempt slept %v, want at least 1s", slept)
	}

	// Each further failure doubles the delay: 2s, 4s, 8s…
	for want := 2 * time.Second; want <= 32*time.Second; want *= 2 {
		d.Failure(context.Background(), "user@example.test")
		slept = nil
		if err := d.Wait(context.Background(), "user@example.test"); err != nil {
			t.Fatal(err)
		}
		if len(slept) != 1 || slept[0] != want {
			t.Fatalf("delay = %v, want %v", slept, want)
		}
	}

	// A long streak stays capped at 60s rather than growing without bound.
	for i := 0; i < 20; i++ {
		d.Failure(context.Background(), "user@example.test")
	}
	slept = nil
	if err := d.Wait(context.Background(), "user@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != time.Minute {
		t.Fatalf("cap = %v, want 60s", slept)
	}
}

func TestLoginDelayWindowExpires(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	slept := 0
	d := NewLoginDelay(LoginDelayConfig{
		Now:   func() time.Time { return now },
		Sleep: func(context.Context, time.Duration) error { slept++; return nil },
	})
	for i := 0; i < 5; i++ {
		d.Failure(context.Background(), "a@b.c")
	}
	now = now.Add(16 * time.Minute) // outside the 15m window
	slept = 0
	if err := d.Wait(context.Background(), "a@b.c"); err != nil {
		t.Fatal(err)
	}
	if slept != 0 {
		t.Errorf("slept %d times after the window expired, want 0", slept)
	}
}

func TestLoginDelaySuccessClears(t *testing.T) {
	t.Parallel()
	slept := 0
	d := NewLoginDelay(LoginDelayConfig{
		Sleep: func(context.Context, time.Duration) error { slept++; return nil },
	})
	for i := 0; i < 10; i++ {
		d.Failure(context.Background(), "a@b.c")
	}
	d.Success(context.Background(), "A@B.C")
	if err := d.Wait(context.Background(), "a@b.c"); err != nil {
		t.Fatal(err)
	}
	if slept != 0 {
		t.Errorf("slept %d times after a success, want 0", slept)
	}
}

func TestLoginDelayContextCancelled(t *testing.T) {
	t.Parallel()
	d := NewLoginDelay(LoginDelayConfig{
		Sleep: func(ctx context.Context, _ time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	for i := 0; i < 5; i++ {
		d.Failure(context.Background(), "a@b.c")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Wait(ctx, "a@b.c"); err == nil {
		t.Fatal("Wait ignored a cancelled context")
	}
}

// TestLoginDelayBoundsTheMap (#1032): a flood of distinct identifiers cannot
// grow the map past its cap, and an identifier already being throttled is the
// last one evicted.
func TestLoginDelayBoundsTheMap(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d := NewLoginDelay(LoginDelayConfig{MaxTracked: 50, Now: func() time.Time { return now }})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		d.Failure(ctx, "target@example.test")
	}
	for i := 0; i < 1000; i++ {
		now = now.Add(time.Millisecond)
		d.Failure(ctx, fmt.Sprintf("junk-%d@example.test", i))
	}
	d.mu.Lock()
	n := len(d.failures)
	d.mu.Unlock()
	if n > 50 {
		t.Fatalf("tracking %d identifiers, cap is 50", n)
	}
}

// TestLoginDelayDropsStaleKeysFirst (#1032): identifiers whose failures left
// the window make room before a live one is evicted.
func TestLoginDelayDropsStaleKeysFirst(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d := NewLoginDelay(LoginDelayConfig{MaxTracked: 3, Window: time.Minute, Now: func() time.Time { return now }})
	ctx := context.Background()
	d.Failure(ctx, "a@x.test")
	d.Failure(ctx, "b@x.test")
	now = now.Add(2 * time.Minute)
	d.Failure(ctx, "live@x.test")
	d.Failure(ctx, "new@x.test")
	d.mu.Lock()
	_, live := d.failures["live@x.test"]
	_, stale := d.failures["a@x.test"]
	d.mu.Unlock()
	if !live || stale {
		t.Errorf("live=%t stale=%t, want the stale keys dropped and the live one kept", live, stale)
	}
}

// TestLoginDelaySharedAcrossReplicas (#1032): with Redis, a failure counted by
// one replica delays the next attempt on another.
func TestLoginDelaySharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	rt, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: fmt.Sprintf("baleia-test-delay-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	slept := 0
	cfg := LoginDelayConfig{Shared: rt, Threshold: 3, Sleep: func(context.Context, time.Duration) error { slept++; return nil }}
	replicaA, replicaB := NewLoginDelay(cfg), NewLoginDelay(cfg)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		replicaA.Failure(ctx, "User@Example.test")
	}
	if err := replicaB.Wait(ctx, "user@example.test"); err != nil || slept != 1 {
		t.Fatalf("replica B slept %d times (err %v), want 1", slept, err)
	}
	replicaB.Success(ctx, "user@example.test")
	if err := replicaA.Wait(ctx, "user@example.test"); err != nil || slept != 1 {
		t.Errorf("a success on one replica must clear it on every replica (slept %d)", slept)
	}
}
