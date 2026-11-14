package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

// defaultMaxTracked bounds how many identifiers one process tracks, so a flood
// of logins with random e-mails cannot grow the map without limit (#1032).
const defaultMaxTracked = 10000

// LoginDelayConfig tunes the progressive delay applied after repeated login
// failures for one account (#51). Zero fields use the defaults.
type LoginDelayConfig struct {
	// Window is how far back failures are counted (default 15m).
	Window time.Duration
	// Threshold is the number of failures before the first delay (default 5).
	Threshold int
	// MaxDelay caps the delay (default 60s).
	MaxDelay time.Duration
	// Shared keeps the failure counters in Redis when it is enabled, so the
	// delay holds across replicas and restarts; a Redis error falls back to the
	// local map (#1032). Nil or disabled keeps them in the process only.
	Shared *realtime.Client
	// MaxTracked caps the identifiers tracked locally (default 10000).
	MaxTracked int
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
	shared    *realtime.Client
	maxKeys   int
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
	if cfg.MaxTracked <= 0 {
		cfg.MaxTracked = defaultMaxTracked
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	return &LoginDelay{
		failures:  make(map[string][]time.Time),
		shared:    cfg.Shared,
		maxKeys:   cfg.MaxTracked,
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
	n, ok := d.sharedCount(ctx, email)
	if !ok {
		d.mu.Lock()
		n = len(d.pruneLocked(email))
		d.mu.Unlock()
	}

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
func (d *LoginDelay) Failure(ctx context.Context, email string) {
	email = normalizeEmail(email)
	if d.shared.Enabled() {
		if _, err := d.shared.CounterIncr(ctx, sharedKey(email), d.window); err == nil {
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, tracked := d.failures[email]; !tracked && len(d.failures) >= d.maxKeys {
		d.makeRoomLocked()
	}
	d.failures[email] = append(d.pruneLocked(email), d.now())
}

// makeRoomLocked frees one slot: it drops every identifier whose failures all
// left the window and, if the map is still full, the one that failed longest
// ago. The caller holds d.mu.
func (d *LoginDelay) makeRoomLocked() {
	var oldest string
	var oldestAt time.Time
	for email := range d.failures {
		recent := d.pruneLocked(email)
		if len(recent) == 0 {
			continue
		}
		if last := recent[len(recent)-1]; oldest == "" || last.Before(oldestAt) {
			oldest, oldestAt = email, last
		}
	}
	if len(d.failures) >= d.maxKeys && oldest != "" {
		delete(d.failures, oldest)
	}
}

// Success clears the failure history for the account.
func (d *LoginDelay) Success(ctx context.Context, email string) {
	email = normalizeEmail(email)
	if d.shared.Enabled() {
		// Best effort: an unreachable Redis must not fail a valid sign-in, and
		// the counter expires with its window.
		_ = d.shared.CounterReset(ctx, sharedKey(email))
	}
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

// sharedCount reads the failures from Redis. ok is false when the shared layer
// is off or errored, and the local map answers instead.
func (d *LoginDelay) sharedCount(ctx context.Context, email string) (int, bool) {
	if !d.shared.Enabled() {
		return 0, false
	}
	n, err := d.shared.CounterGet(ctx, sharedKey(email))
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// sharedKey hides the identifier: Redis holds a digest, never an e-mail.
func sharedKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return "logindelay:" + hex.EncodeToString(sum[:])
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
			h.loginDelayStore = NewLoginDelay(LoginDelayConfig{Shared: h.rt})
		}
	})
	return h.loginDelayStore
}
