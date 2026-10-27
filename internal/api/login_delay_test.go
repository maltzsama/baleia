package api

import (
	"context"
	"testing"
	"time"
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
		d.Failure("User@Example.test")
	}
	if err := d.Wait(context.Background(), "user@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] < time.Second {
		t.Fatalf("6th attempt slept %v, want at least 1s", slept)
	}

	// Each further failure doubles the delay: 2s, 4s, 8s…
	for want := 2 * time.Second; want <= 32*time.Second; want *= 2 {
		d.Failure("user@example.test")
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
		d.Failure("user@example.test")
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
		d.Failure("a@b.c")
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
		d.Failure("a@b.c")
	}
	d.Success("A@B.C")
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
		d.Failure("a@b.c")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Wait(ctx, "a@b.c"); err == nil {
		t.Fatal("Wait ignored a cancelled context")
	}
}
