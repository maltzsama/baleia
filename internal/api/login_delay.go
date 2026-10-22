package api

import (
	"context"
	"strings"
	"sync"
	"time"
)

// LoginDelayConfig tunes the progressive delay applied after repeated login
// failures for one account (#51). Zero fields use the defaults.
type LoginDelayConfig struct {
	// Window is how far back failures are counted (default 15m).
	Window time.Duration
	// Threshold is the number of failures before the first delay (default 5).
	Threshold int
	// MaxDelay caps the delay (default 60s).
	MaxDelay time.Duration
	// Now and Sleep are injectable for tests.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

// LoginDelay tracks login failures per normalized e-mail and sleeps
// proportionally before the next attempt. It never locks an account
// permanently.
type LoginDelay struct {
	mu        sync.Mutex
	failures  map[string][]time.Time
	window    time.Duration
	threshold int
	maxDelay  time.Duration
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
}

// NewLoginDelay builds a delay store with the given configuration.
func NewLoginDelay(cfg LoginDelayConfig) *LoginDelay {
	if cfg.Window <= 0 {
		cfg.Window = 15 * time.Minute
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 60 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	return &LoginDelay{
		failures:  make(map[string][]time.Time),
		window:    cfg.Window,
		threshold: cfg.Threshold,
		maxDelay:  cfg.MaxDelay,
		now:       cfg.Now,
		sleep:     cfg.Sleep,
	}
}

// Wait sleeps before evaluating an attempt when the account already has
// Threshold recent failures. The delay is min(2^(n-Threshold), MaxDelay)
// seconds. It returns the context error if the client goes away while waiting.
func (d *LoginDelay) Wait(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	d.mu.Lock()
	n := len(d.pruneLocked(email))
	d.mu.Unlock()

	if n < d.threshold {
		return nil
	}
	delay := d.maxDelay
	if shift := n - d.threshold; shift < 6 {
		if candidate := time.Duration(1<<shift) * time.Second; candidate < delay {
			delay = candidate
		}
	}
	return d.sleep(ctx, delay)
}

// Failure records one failed attempt for the account.
func (d *LoginDelay) Failure(email string) {
	email = normalizeEmail(email)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failures[email] = append(d.pruneLocked(email), d.now())
}

// Success clears the failure history for the account.
func (d *LoginDelay) Success(email string) {
	email = normalizeEmail(email)
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.failures, email)
}

// pruneLocked drops failures older than the window and returns the recent
// ones. The caller holds d.mu.
func (d *LoginDelay) pruneLocked(email string) []time.Time {
	cutoff := d.now().Add(-d.window)
	old := d.failures[email]
	recent := old[:0]
	for _, t := range old {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(d.failures, email)
		return nil
	}
	d.failures[email] = recent
	return recent
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// loginDelay returns the handler's delay store, creating it on first use.
func (h *AuthHandlers) loginDelay() *LoginDelay {
	h.loginDelayOnce.Do(func() {
		if h.loginDelayStore == nil {
			h.loginDelayStore = NewLoginDelay(LoginDelayConfig{})
		}
	})
	return h.loginDelayStore
}
